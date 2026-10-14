/* A small, dependency-free IndexedDB key/value store.

   The offline adapters (mock and PGlite) keep learner data here: `progress` is
   keyed by course id and holds one snapshot per course, `events` is keyed by
   event id and only ever grows. The gateway is still the authoritative store
   whenever a server is present; this is what lets the offline workspace
   actually survive a reload instead of pretending a write succeeded.

   Every failure path is a typed `IdbError` carrying the code
   PERSISTENCE_UNAVAILABLE — including "this browser has no IndexedDB"
   (private mode, disabled storage) and "another tab blocked the upgrade". An
   open that never resolves is not an acceptable outcome, so the open is raced
   against a timeout. */

export const IDB_NAME = 'querycraft';
/** Bumped to 2 when the offline layout was aligned with design 8.1. */
export const IDB_VERSION = 2;

/* Design 8.1: the browser-mode stores, by name. The progress snapshot stays in
   `progress` (one read loads the workspace), and the adapter splits the same
   snapshot across the per-entity stores so bookmarks, notes, saved queries and
   attempts are addressable on their own. */
export const STORE_ATTEMPTS = 'attempts';
export const STORE_EVALUATIONS = 'evaluations';
export const STORE_PROGRESS = 'progress';
export const STORE_NOTES = 'notes';
export const STORE_BOOKMARKS = 'bookmarks';
export const STORE_SAVED_QUERIES = 'saved_queries';
export const STORE_DRAFTS = 'drafts';
export const STORE_META = 'meta';

/** Every store the browser-mode workspace owns, in 8.1 order. Created on
    upgrade, so a database from an earlier version is migrated rather than
    silently missing a store the adapter is about to write to. */
export const OFFLINE_STORES: readonly string[] = [
  STORE_ATTEMPTS,
  STORE_EVALUATIONS,
  STORE_PROGRESS,
  STORE_NOTES,
  STORE_BOOKMARKS,
  STORE_SAVED_QUERIES,
  STORE_DRAFTS,
  STORE_META,
];

/** How long to wait for `indexedDB.open` before declaring storage unusable. */
const OPEN_TIMEOUT_MS = 4000;

/** A storage failure the adapters can turn into a typed GatewayError. */
export class IdbError extends Error {
  readonly code = 'PERSISTENCE_UNAVAILABLE' as const;

  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'IdbError';
  }
}

let dbPromise: Promise<IDBDatabase> | null = null;

/** Open (and memoize) the database. A failure clears the cache so a later call
    can retry once the learner frees space or leaves private mode. */
export function openDatabase(): Promise<IDBDatabase> {
  if (!dbPromise) {
    dbPromise = openWithTimeout().catch((error: unknown) => {
      dbPromise = null;
      throw error;
    });
  }
  return dbPromise;
}

function openWithTimeout(): Promise<IDBDatabase> {
  return new Promise<IDBDatabase>((resolve, reject) => {
    const factory: IDBFactory | undefined = typeof indexedDB === 'undefined' ? undefined : indexedDB;
    if (!factory) {
      reject(new IdbError('This browser does not expose IndexedDB, so progress cannot be stored locally.'));
      return;
    }

    let settled = false;
    const finish = (settle: () => void): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      settle();
    };
    const timer = setTimeout(
      () => finish(() => reject(new IdbError('Opening the local database timed out; storage may be blocked or unavailable.'))),
      OPEN_TIMEOUT_MS,
    );

    let request: IDBOpenDBRequest;
    try {
      request = factory.open(IDB_NAME, IDB_VERSION);
    } catch (cause) {
      finish(() => reject(new IdbError('The browser refused to open local storage (it may be disabled).', { cause })));
      return;
    }

    request.onupgradeneeded = () => {
      const db = request.result;
      for (const name of OFFLINE_STORES) {
        if (!db.objectStoreNames.contains(name)) db.createObjectStore(name);
      }
    };
    request.onsuccess = () => {
      const db = request.result;
      /* The timeout or a blocked upgrade already answered; don't leak the handle. */
      if (settled) {
        db.close();
        return;
      }
      db.onversionchange = () => {
        db.close();
        dbPromise = null;
      };
      finish(() => resolve(db));
    };
    request.onerror = () =>
      finish(() => reject(new IdbError('The local database could not be opened.', { cause: request.error })));
    request.onblocked = () =>
      finish(() => reject(new IdbError('Another tab is holding an older version of the local database open.')));
  });
}

/** Run one request in a fresh transaction and resolve only once the
    transaction has committed, so a resolved promise means the write landed. */
async function run<T>(
  store: string,
  mode: IDBTransactionMode,
  op: (s: IDBObjectStore) => IDBRequest<T> | null,
): Promise<T | undefined> {
  const db = await openDatabase();
  return new Promise<T | undefined>((resolve, reject) => {
    let tx: IDBTransaction;
    try {
      tx = db.transaction(store, mode);
    } catch (cause) {
      reject(new IdbError(`The local database could not open the "${store}" store.`, { cause }));
      return;
    }

    let result: T | undefined;
    tx.oncomplete = () => resolve(result);
    tx.onerror = () => reject(new IdbError(`A local database transaction failed (${store}).`, { cause: tx.error }));
    tx.onabort = () => reject(new IdbError(`A local database transaction was aborted (${store}).`, { cause: tx.error }));

    try {
      const req = op(tx.objectStore(store));
      if (req) req.onsuccess = () => { result = req.result; };
    } catch (cause) {
      try {
        tx.abort();
      } catch {
        /* already settled */
      }
      reject(new IdbError(`The local database rejected a request on "${store}".`, { cause }));
    }
  });
}

/** Read one value, or `undefined` when the key is absent. */
export async function idbGet<T>(store: string, key: string): Promise<T | undefined> {
  return run<T>(store, 'readonly', (s) => s.get(key));
}

/** Write one value. Resolves only after the transaction commits. */
export async function idbPut(store: string, key: string, value: unknown): Promise<void> {
  await run<IDBValidKey>(store, 'readwrite', (s) => s.put(value, key));
}

/** Write several rows in ONE transaction, so a bundle either lands whole or not
    at all (design 8.1: writes use transactions). A half-written snapshot -- a
    note stored while its bookmark is lost -- is exactly what this prevents. */
export async function idbPutMany(
  rows: ReadonlyArray<readonly [store: string, key: string, value: unknown]>,
): Promise<void> {
  if (rows.length === 0) return;
  const db = await openDatabase();
  const stores = Array.from(new Set(rows.map(([store]) => store)));
  return new Promise<void>((resolve, reject) => {
    let tx: IDBTransaction;
    try {
      tx = db.transaction(stores, 'readwrite');
    } catch (cause) {
      reject(new IdbError(`The local database could not open ${stores.join(', ')}.`, { cause }));
      return;
    }
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(new IdbError('A local database bundle write failed.', { cause: tx.error }));
    tx.onabort = () => reject(new IdbError('A local database bundle write was aborted.', { cause: tx.error }));
    try {
      for (const [store, key, value] of rows) tx.objectStore(store).put(value, key);
    } catch (cause) {
      try {
        tx.abort();
      } catch {
        /* already settled */
      }
      reject(new IdbError('The local database rejected a bundle write.', { cause }));
    }
  });
}

export async function idbDelete(store: string, key: string): Promise<void> {
  await run<undefined>(store, 'readwrite', (s) => s.delete(key));
}

/** Every key/value pair in a store, ordered by key. */
export async function idbEntries<T>(store: string): Promise<Array<[string, T]>> {
  const db = await openDatabase();
  return new Promise<Array<[string, T]>>((resolve, reject) => {
    let tx: IDBTransaction;
    try {
      tx = db.transaction(store, 'readonly');
    } catch (cause) {
      reject(new IdbError(`The local database could not open the "${store}" store.`, { cause }));
      return;
    }
    const objectStore = tx.objectStore(store);
    const keys = objectStore.getAllKeys();
    const values = objectStore.getAll();
    tx.oncomplete = () => resolve(keys.result.map((key, i) => [String(key), values.result[i] as T] as [string, T]));
    tx.onerror = () => reject(new IdbError(`Reading the local "${store}" store failed.`, { cause: tx.error }));
    tx.onabort = () => reject(new IdbError(`Reading the local "${store}" store was aborted.`, { cause: tx.error }));
  });
}
