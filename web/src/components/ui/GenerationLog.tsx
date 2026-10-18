/*
 * The generation stream: one paced line per entry, each with its own timestamp.
 *
 * Both the build screen and the challenge lab render the gateway's own
 * transcript under this markup, so the shape lives here once. Lines arrive
 * already escaped by the caller, which is what the `dangerouslySetInnerHTML` is
 * for.
 */
export default function GenerationLog({ log, open }: { log: { t: string; html: string }[]; open: boolean }) {
  return (
    <div className={`stream${open ? ' open' : ''}`}>
      {log.map((l, i) => (
        <div className="ln" key={i}><span className="t">{l.t}</span><span dangerouslySetInnerHTML={{ __html: l.html }} /></div>
      ))}
    </div>
  );
}
