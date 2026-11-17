import { useState, type FormEvent } from 'react';
import { api, errorText, type Room } from './api';

export function MessageReport({room, messageId, getRoomSignal}: {
  room: Room;
  messageId: number;
  getRoomSignal: () => AbortSignal | undefined;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [receipt, setReceipt] = useState<number>();
  const [error, setError] = useState('');

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const signal = getRoomSignal();
    if (!signal || signal.aborted || busy) return;
    const reason = new FormData(event.currentTarget).get('reason');
    setBusy(true);
    setError('');
    try {
      const result = await api<{id: number}>(`/rooms/${room.id}/messages/${messageId}/reports`, 'POST', {reason}, signal, room.membershipId);
      if (!signal.aborted) setReceipt(result.id);
    } catch (err) {
      if (!signal.aborted) setError(errorText(err));
    } finally {
      if (!signal.aborted) setBusy(false);
    }
  }

  if (receipt !== undefined) return <p className="message-report subtle" role="status">已提交举报，编号 {receipt}</p>;
  return <div className="message-report">
    {open ? <form className="stack" aria-label="举报此消息" onSubmit={submit}>
      <p className="subtle">提交后，本实例的维护者可查看这条消息。</p>
      <label>举报原因<select name="reason" required defaultValue="">
        <option value="" disabled>请选择</option>
        <option value="spam">垃圾信息</option>
        <option value="harassment">骚扰</option>
        <option value="other">其他</option>
      </select></label>
      {error && <p className="error" role="alert">{error}</p>}
      <div className="row">
        <button className="primary" disabled={busy}>{busy ? '提交中…' : '提交举报'}</button>
        <button className="secondary" type="button" disabled={busy} onClick={() => {setOpen(false);setError('');}}>取消</button>
      </div>
    </form> : <button className="text-button" type="button" aria-label="举报此消息" onClick={() => setOpen(true)}>举报</button>}
  </div>;
}
