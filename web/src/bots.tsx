import {useEffect, useRef, useState, type FormEvent} from 'react';
import {api, errorText, RequestError, type Room} from './api';

type Bot = {id: string; displayName: string};
type InstallationState = 'active' | 'pending';
type Grant = {installationId: string; permissions: string[]};
type OwnedInstallation = Grant & {roomId: number; requestedBy: string; state: InstallationState};
type GroupInstallation = Grant & {botId: string; displayName: string; state?: InstallationState};
type InstallationPreview = {
  botId: string; displayName: string; permissions: string[]; state: InstallationState | 'available'; installationId?: string;
};

const installationState = {active: '已生效', pending: '待 Bot 所有者审批', available: '可安装'};

function useBotList<T>(path: string, version: number, membershipId?: string) {
  const [result, setResult] = useState<{items?: T[]; error?: unknown}>({});
  useEffect(() => {
    const controller = new AbortController();
    api<T[]>(path, 'GET', undefined, controller.signal, membershipId)
      .then(items => {if (!controller.signal.aborted) setResult({items});})
      .catch(error => {if (!controller.signal.aborted) setResult({error});});
    return () => controller.abort();
  }, [path, version, membershipId]);
  return result;
}

function useBotAction() {
  const current = useRef<AbortController | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => () => current.current?.abort(), []);

  async function run<T>(
    request: (signal: AbortSignal) => Promise<T>,
    onSuccess: (result: T) => void,
    onFailure?: () => void,
  ) {
    if (current.current) return;
    const controller = new AbortController();
    current.current = controller;
    setBusy(true);
    setError('');
    try {
      const result = await request(controller.signal);
      if (!controller.signal.aborted) onSuccess(result);
    } catch (err) {
      if (!controller.signal.aborted) {
        setError(errorText(err));
        onFailure?.();
      }
    } finally {
      current.current = null;
      if (!controller.signal.aborted) setBusy(false);
    }
  }

  return {busy, error, run};
}

function BotListStatus({items, error, empty}: {items?: unknown[]; error?: unknown; empty: string}) {
  if (error) return <p role="alert" className="error">{errorText(error)}</p>;
  if (!items) return <p role="status" className="subtle">正在读取…</p>;
  return items.length ? null : <p className="subtle">{empty}</p>;
}

function BotPermissions({permissions}: {permissions: string[]}) {
  return <ul className="bot-permissions" aria-label="授权权限">
    {permissions.map(permission => <li key={permission}>
      {permission === 'installation:read' ? '查看本群的 Bot 安装状态' : permission}
    </li>)}
  </ul>;
}

function TokenDisplay({token, onDismiss}: {token: string; onDismiss: () => void}) {
  const field = useRef<HTMLInputElement>(null);
  const [copyStatus, setCopyStatus] = useState('');
  async function copy() {
    try {
      await navigator.clipboard.writeText(token);
      setCopyStatus('凭据已复制');
    } catch {
      field.current?.focus();
      field.current?.select();
      setCopyStatus('请手动复制选中的凭据');
    }
  }
  return <div className="bot-secret stack">
    <label>本次 Bot 凭据<input ref={field} value={token} readOnly spellCheck={false} autoComplete="off" /></label>
    <p className="subtle">请现在保存在 Bot 运行环境，关闭后需要重新签发才能取得新凭据。</p>
    <div className="row">
      <button type="button" className="secondary" onClick={copy}>复制凭据</button>
      <button type="button" className="secondary" onClick={onDismiss}>隐藏凭据</button>
    </div>
    {copyStatus && <p role="status" className="subtle">{copyStatus}</p>}
  </div>;
}

function OwnedBotDetails({bot, version, onChange}: {bot: Bot; version: number; onChange: () => void}) {
  const base = '/bots/' + encodeURIComponent(bot.id);
  const {items, error: listError} = useBotList<OwnedInstallation>(base + '/installations', version);
  const {busy, error, run} = useBotAction();
  const [token, setToken] = useState('');
  const [notice, setNotice] = useState('');

  function rotate() {
    setToken('');
    setNotice('');
    void run(signal => api<{token: string}>(base + '/credential/rotate', 'POST', undefined, signal), result => {
      setToken(result.token);
    });
  }

  function revoke() {
    setToken('');
    setNotice('');
    void run(signal => api(base + '/credential', 'DELETE', undefined, signal), () => setNotice('凭据已撤销'));
  }

  function changeInstallation(installation: OwnedInstallation, approve: boolean) {
    const id = encodeURIComponent(installation.installationId);
    const path = approve ? base + '/installations/' + id + '/approve' : '/bot-installations/' + id;
    void run(signal => api(path, approve ? 'POST' : 'DELETE', undefined, signal), onChange, onChange);
  }

  return <section className="bot-detail stack" aria-label="当前 Bot">
    <h3>{bot.displayName}</h3>
    <label>Bot ID<input value={bot.id} readOnly spellCheck={false} /></label>
    <div className="row">
      <button type="button" className="secondary" disabled={busy} onClick={rotate}>签发或轮换凭据</button>
      <button type="button" className="secondary" disabled={busy} onClick={revoke}>撤销凭据</button>
    </div>
    <p className="subtle">轮换或撤销会让上一份凭据失效。</p>
    {token && <TokenDisplay token={token} onDismiss={() => setToken('')} />}
    {notice && <p role="status" className="subtle">{notice}</p>}
    {error && <p role="alert" className="error">{error}</p>}
    <h3>群安装</h3>
    <BotListStatus items={items} error={listError} empty="暂无群安装。" />
    {items && items.length > 0 && <ul className="bot-list">
      {items.map(item => <li key={item.installationId} className="stack">
        <div><strong>群 {item.roomId}</strong><small>{'@' + item.requestedBy + ' · ' + installationState[item.state]}</small></div>
        <BotPermissions permissions={item.permissions} />
        <div className="row">
          {item.state === 'pending' && <button type="button" className="primary" disabled={busy}
            onClick={() => changeInstallation(item, true)}>批准安装</button>}
          <button type="button" className="secondary" disabled={busy} onClick={() => changeInstallation(item, false)}>
            {item.state === 'pending' ? '拒绝安装' : '移除安装'}
          </button>
        </div>
      </li>)}
    </ul>}
  </section>;
}

export function BotManager({version, onChange, onClose}: {version: number; onChange: () => void; onClose: () => void}) {
  const {items, error: listError} = useBotList<Bot>('/bots', version);
  const {busy, error, run} = useBotAction();
  const [selectedId, setSelectedId] = useState<string>();
  const selected = selectedId === undefined ? items?.[0] : items?.find(item => item.id === selectedId);

  function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const displayName = new FormData(form).get('displayName');
    void run(signal => api<Bot>('/bots', 'POST', {displayName}, signal), bot => {
      form.reset();
      setSelectedId(bot.id);
      onChange();
    }, onChange);
  }

  return <section className="bot-manager" aria-labelledby="bot-manager-title">
    <header className="bot-manager-header">
      <h2 id="bot-manager-title">管理 Bot</h2>
      <button type="button" className="secondary" onClick={onClose}>返回会话</button>
    </header>
    <div className="bot-manager-grid">
      <div className="stack">
        <form className="stack" onSubmit={create}>
          <label>Bot 名称<input name="displayName" required maxLength={96} /></label>
          <button className="primary" disabled={busy}>创建 Bot</button>
        </form>
        {error && <p role="alert" className="error">{error}</p>}
        <BotListStatus items={items} error={listError} empty="还没有 Bot。" />
        {items && items.length > 0 && <ul className="bot-list" aria-label="我的 Bot">
          {items.map(bot => <li key={bot.id}><button type="button" className="secondary full" aria-pressed={selected?.id === bot.id}
            onClick={() => setSelectedId(bot.id)}>{bot.displayName}</button></li>)}
        </ul>}
      </div>
      {selected && <OwnedBotDetails key={selected.id} bot={selected} version={version} onChange={onChange} />}
    </div>
  </section>;
}

export function GroupBots({room, canManage, version, onChange, onAccessLost}: {
  room: Room; canManage: boolean; version: number; onChange: () => void; onAccessLost: () => void;
}) {
  const base = '/rooms/' + room.id;
  const listPath = base + '/bot-installations' + (canManage ? '?includePending=1' : '');
  const {items, error: listError} = useBotList<GroupInstallation>(listPath, version, room.membershipId);
  const {busy, error, run} = useBotAction();
  const [botId, setBotId] = useState('');
  const [preview, setPreview] = useState<InstallationPreview>();

  useEffect(() => {
    if (listError instanceof RequestError && listError.status === 404) onAccessLost();
  }, [listError, onAccessLost]);

  function previewInstall(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPreview(undefined);
    const id = botId.trim();
    if (!id) return;
    void run(signal => api<InstallationPreview>(base + '/bots/' + encodeURIComponent(id) + '/installation',
      'GET', undefined, signal, room.membershipId), setPreview);
  }

  function install(current: InstallationPreview) {
    const path = base + '/bots/' + encodeURIComponent(current.botId) + '/installation';
    void run(signal => api<InstallationPreview>(path, 'PUT', {permissions: current.permissions}, signal, room.membershipId), () => {
      setPreview(undefined);
      setBotId('');
      onChange();
    }, onChange);
  }

  function remove(item: GroupInstallation) {
    void run(signal => api('/bot-installations/' + encodeURIComponent(item.installationId),
      'DELETE', undefined, signal, room.membershipId), () => {
      setPreview(undefined);
      onChange();
    }, onChange);
  }

  return <aside className="group-bots stack" aria-label="群 Bot">
    <BotListStatus items={items} error={listError} empty="本群暂无 Bot 安装。" />
    {items && items.length > 0 && <ul className="bot-list">
      {items.map(item => <li key={item.installationId} className="stack">
        <div><strong>{item.displayName}</strong><small>{installationState[item.state ?? 'active']}</small></div>
        <BotPermissions permissions={item.permissions} />
        {canManage && <div className="row"><button type="button" className="secondary" disabled={busy}
          onClick={() => remove(item)}>{item.state === 'pending' ? '撤回安装' : '移除安装'}</button></div>}
      </li>)}
    </ul>}
    {canManage && <form className="stack" onSubmit={previewInstall}>
      <label>待安装 Bot ID<input value={botId} required spellCheck={false}
        onChange={event => {setBotId(event.target.value);setPreview(undefined);}} disabled={busy} /></label>
      <div className="row"><button className="secondary" disabled={busy || !botId.trim()}>查看 Bot 权限</button></div>
    </form>}
    {canManage && preview && <div className="bot-preview stack" aria-label="Bot 安装预览">
      <div><strong>{preview.displayName}</strong><small>{installationState[preview.state]}</small></div>
      <BotPermissions permissions={preview.permissions} />
      <div className="row">
        {preview.state === 'available' && <button type="button" className="primary" disabled={busy}
          onClick={() => install(preview)}>确认安装</button>}
        <button type="button" className="secondary" disabled={busy} onClick={() => setPreview(undefined)}>关闭预览</button>
      </div>
    </div>}
    {error && <p role="alert" className="error">{error}</p>}
  </aside>;
}
