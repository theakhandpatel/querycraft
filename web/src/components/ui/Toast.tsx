import { useStore } from '../../state/store';

export default function Toast() {
  const { toastMsg } = useStore();
  return <div className={`toast${toastMsg ? ' show' : ''}`}>{toastMsg}</div>;
}
