import { StrictMode, useEffect, useRef, useState, type FormEvent } from 'react';
import { createRoot } from 'react-dom/client';
import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { api, errorText, openServer, RequestError, type Account, type Info, type Message, type Page, type Room } from './api';
import { clearDrafts, draftKey, readDraft, retainDrafts, saveDraft, type Draft } from './drafts';
import './style.css';

const authErrors: Record<string, string> = {
  invalid: 'Radish 登录未能验证，请重新发起。', cancelled: 'Radish 登录已取消。',
  unavailable: 'Radish 登录暂不可用。', closed: '此服务器未开放新账号注册。',
};
const formatter = new Intl.DateTimeFormat('zh-CN', {month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit'});
const maxMarkdownCodePoints = 8000;
const first = (name: string) => Array.from(name)[0]?.toUpperCase() || '·';

function Brand() {
  return <div className="brand"><span aria-hidden="true" className="brand-mark">
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none"><path d="M5 5h14v10H9l-4 4V5Z" stroke="currentColor" strokeWidth="1.75" strokeLinejoin="round"/><path d="M9 10h6" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round"/></svg>
  </span><span>RadishChat <small>萝卜聊天</small></span></div>;
}

function ServerPicker() {
  const [shown, setShown] = useState(false);
  const [error, setError] = useState('');
  return <div className="server-picker">
    <button type="button" className="text-button" aria-expanded={shown} onClick={() => setShown(!shown)}>切换服务器</button>
    {shown && <form className="server-form" onSubmit={(e) => {
      e.preventDefault(); setError('');
      try { openServer(String(new FormData(e.currentTarget).get('server') || '')); }
      catch (err) { setError((err as Error).message); }
    }}>
      <label>服务器地址<input name="server" autoComplete="url" placeholder="https://chat.example.com" required /></label>
      <button className="primary">打开</button>
      {error && <span role="alert" className="error">{error}</span>}
    </form>}
  </div>;
}

function Access({info, onLogin, onInfo}: {info: Info; onLogin: (a: Account) => void; onInfo: (info: Info) => void}) {
  const [register, setRegister] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(() => {
    const key = new URLSearchParams(window.location.search).get('login');
    return key && Object.hasOwn(authErrors, key) ? authErrors[key] : '';
  });
  useEffect(() => { if (window.location.search) window.history.replaceState({}, '', '/'); }, []);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const values = Object.fromEntries(new FormData(e.currentTarget));
    setBusy(true); setError('');
    try { onLogin(await api<Account>(register ? '/register' : '/login', 'POST', values)); }
    catch (err) {
      setError(errorText(err));
      if (register && err instanceof RequestError && err.status === 403) {
        const current = await api<Info>('/info').catch(() => null);
        if (current) {
          onInfo(current);
          if (!current.registration) setRegister(false);
        }
      }
    }
    finally { setBusy(false); }
  }
  async function radishLogin() {
    setBusy(true); setError('');
    try {
      const {authorizationUrl} = await api<{authorizationUrl: string}>('/auth/radish/start', 'POST');
      window.location.assign(authorizationUrl);
    } catch (err) {setError(errorText(err)); setBusy(false);}
  }
  return <main className="access">
    <header><Brand /></header>
    <div className="access-body">
      <div className="intro">
        <span className="eyebrow">私聊 · 群组 · Markdown</span>
        <h1>把对话留在<br />自己的服务器。</h1>
        <p>和朋友聊几句，给项目建个群。用 Markdown 写清楚代码、列表和那些值得留住的细节。</p>
        <div className="server-name"><span className="dot"/> 当前服务器 <strong>{window.location.host}</strong></div>
        <ServerPicker />
      </div>
      <section className="card access-card" aria-label="账号">
        <h2>{register ? '创建本地账号' : '登录聊天'}</h2>
        <p className="subtle">{register ? '账号只属于当前服务器。' : '使用当前服务器的账号继续。'}</p>
        <form className="stack" key={String(register)} onSubmit={submit}>
          <label>用户名<input name="username" autoComplete="username" required maxLength={32} placeholder="例如 radish_dev" /></label>
          {register && <label>昵称 <small>可选</small><input name="displayName" autoComplete="nickname" maxLength={48} placeholder="别人看到的名字" /></label>}
          <label>密码<input name="password" type="password" autoComplete={register ? 'new-password' : 'current-password'} minLength={register ? 10 : undefined} required placeholder={register ? '至少 10 个字符' : '输入密码'}/></label>
          {register && info.registrationInviteRequired && <label>邀请码<input name="inviteCode" type="password" autoComplete="off" maxLength={48} required /></label>}
          <button className="primary" disabled={busy}>{register ? '注册并登录' : '登录'}</button>
        </form>
        {info.radishLogin && <button className="secondary full" disabled={busy} type="button" onClick={radishLogin}>使用 Radish 账号登录</button>}
        {error && <p role="alert" className="error">{error}</p>}
        {info.registration && <button className="text-button" type="button" disabled={busy} onClick={() => {setRegister(!register);setError('');}}>{register ? '已有账号？返回登录' : '没有账号？在此服务器注册'}</button>}
      </section>
    </div>
  </main>;
}

function renderURL(url: string, key: string) {
  if (key !== 'href') return '';
  try { return ['https:', 'http:'].includes(new URL(url).protocol) ? url : ''; }
  catch { return ''; }
}
function MessageBody({body}: {body: string}) {
  return <Markdown skipHtml remarkPlugins={[remarkGfm]} urlTransform={renderURL} components={{
    a: ({href, children}) => href ? <a href={href} target="_blank" rel="noreferrer noopener">{children}</a> : <span>{children}</span>,
    img: ({alt}) => <span>{alt ? `[图片：${alt}]` : '[图片]'}</span>,
  }}>{body}</Markdown>;
}

function NewRoom({kind, onCancel, onOpen}: {kind: Room['kind']; onCancel: () => void; onOpen: (id: number) => void}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  return <form className="new-room stack" onSubmit={async (e) => {
    e.preventDefault();
    const values = new FormData(e.currentTarget);
    setBusy(true);setError('');
    try {
      const {id} = await api<{id: number}>(`/rooms/${kind}`, 'POST', kind === 'direct' ? {username: values.get('username')} :
        {name: values.get('name'), usernames: String(values.get('usernames') ?? '').split(/[\s,，、]+/).filter(Boolean)});
      onOpen(id);
    } catch (err) {setError(errorText(err));}
    finally {setBusy(false);}
  }}>
    {kind === 'direct' ? <label>对方用户名<input autoFocus name="username" required maxLength={32} placeholder="完整用户名" /></label> :
      <><label>群名称<input autoFocus name="name" required maxLength={64} /></label>
      <label>初始成员 <small>可选</small><input name="usernames" placeholder="用户名，用逗号隔开" /></label></>}
    <div className="row"><button className="primary" disabled={busy}>{kind === 'direct' ? '打开私聊' : '创建群组'}</button><button type="button" className="secondary" disabled={busy} onClick={onCancel}>取消</button></div>
    {error && <span className="error" role="alert">{error}</span>}
  </form>;
}

function Members({room, me, version, getRoomSignal, onChange, onAccessLost}: {
  room: Room; me: Account; version: number; getRoomSignal: () => AbortSignal | undefined; onChange: () => void; onAccessLost: () => void;
}) {
  const [members, setMembers] = useState<Account[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const canEdit = room.ownerId === me.id;
  useEffect(() => {
    const controller = new AbortController();
    const signal = getRoomSignal();
    api<Account[]>(`/rooms/${room.id}/members`, 'GET', undefined, controller.signal, room.membershipId)
      .then(result => { if (!controller.signal.aborted && !signal?.aborted) setMembers(result); })
      .catch(err => {
        if (controller.signal.aborted || signal?.aborted) return;
        if (err instanceof RequestError && err.status === 404) onAccessLost();
        else setError(errorText(err));
      });
    return () => controller.abort();
  }, [room.id, room.membershipId, version]);
  async function change(path: string, method: string, data?: unknown, didChange = onChange) {
    const signal = getRoomSignal();
    setBusy(true);setError('');
    try {
      await api(path, method, data, signal, room.membershipId);
      if (signal?.aborted) return false;
      didChange();
      return true;
    } catch (err) {
      if (!signal?.aborted) {
        if (err instanceof RequestError && err.status === 404) onChange();
        setError(errorText(err));
      }
      return false;
    } finally { if (!signal?.aborted) setBusy(false); }
  }
  return <aside className="members" aria-label="群成员">
    <ul>{members.map((m) => <li key={m.id}><span><strong>{m.displayName}</strong><small>@{m.username}{room.ownerId === m.id && ' · 群主'}</small></span>
      {canEdit && m.id !== me.id && <button type="button" className="text-button" disabled={busy} aria-label={`移除 ${m.displayName}`} onClick={() => void change(`/rooms/${room.id}/members/${m.id}`, 'DELETE')}>移除</button>}</li>)}</ul>
    {canEdit && <form className="stack" onSubmit={async (e) => {
      e.preventDefault();const form = e.currentTarget;
      if (await change(`/rooms/${room.id}/members`, 'POST', {username: new FormData(form).get('member')})) form.reset();
    }}><label>添加成员<input name="member" placeholder="完整用户名" required maxLength={32} /></label><button className="secondary" disabled={busy}>添加</button></form>}
    {!canEdit && <button type="button" className="secondary" disabled={busy} onClick={() => void change(`/rooms/${room.id}/leave`, 'POST', undefined, onAccessLost)}>退出群组</button>}
    {error && <p role="alert" className="error">{error}</p>}
  </aside>;
}

function Conversation({me, room, version, onChange, onBack, onAccessLost}: {
  me: Account; room: Room; version: number; onChange: () => void; onBack: () => void; onAccessLost: () => void;
}) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [more, setMore] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [earlier, setEarlier] = useState(false);
  const [members, setMembers] = useState(false);
  const [preview, setPreview] = useState(false);
  const storageKey = draftKey(me.id, room);
  const [draft, setDraft] = useState(() => readDraft(storageKey));
  const {text} = draft;
  const [sending, setSending] = useState(false);
  const [notice, setNotice] = useState('');
  const [sendError, setSendError] = useState('');
  const tooLong = Array.from(text).length > maxMarkdownCodePoints;
  const composerError = tooLong ? '消息最多 8,000 个字符' : sendError;
  const feed = useRef<HTMLDivElement>(null);
  // null 表示尚未读取，0 表示已经确认没有可见消息。
  const newest = useRef<number | null>(null);
  const oldest = useRef<number | null>(null);
  const paging = useRef<{controller: AbortController; refresh: () => Promise<void>} | null>(null);
  const pinBottom = useRef(true);
  const preserve = useRef(0);

  function updateDraft(next: Draft) {
    setDraft(next);
    saveDraft(storageKey, next);
  }

  function merge(next: Message[]) {
    if (next.length === 0) return;
    setMessages((current) => Array.from(new Map([...current, ...next].map(m => [m.id, m])).values()).sort((a,b) => a.id - b.id));
  }

  function showReadError(err: unknown) {
    if (err instanceof RequestError && err.status === 404) {
      loseAccess();
    } else setNotice(errorText(err));
  }

  function loseAccess() {
    paging.current?.controller.abort();
    onAccessLost();
  }

  useEffect(() => {
    const controller = new AbortController();
    const {signal} = controller;
    const path = `/rooms/${room.id}/messages`;
    let running = false;
    let pending = false;

    async function readForward() {
      try {
        let moreAhead = false;
        do {
          const after = newest.current;
          const result = await api<Page>(after === null ? path : `${path}?after=${after}`, 'GET', undefined, signal, room.membershipId);
          if (signal.aborted) return;
          if (after === null) {
            oldest.current = result.messages[0]?.id ?? null;
            setMore(result.more);
          }
          newest.current = result.messages.at(-1)?.id ?? after ?? 0;
          merge(result.messages);
          setLoaded(true);
          setNotice('');
          moreAhead = after !== null && result.more && result.messages.length > 0;
        } while (moreAhead);
      } catch (err) {
        if (!signal.aborted) showReadError(err);
      }
    }

    async function refresh() {
      pending = true;
      if (running) return;
      running = true;
      try {
        while (pending && !signal.aborted) {
          pending = false;
          await readForward();
        }
      } finally {
        running = false;
      }
    }

    paging.current = {controller, refresh};
    return () => {
      controller.abort();
      paging.current = null;
    };
  }, [room.id, room.membershipId]);
  useEffect(() => {
    void paging.current?.refresh();
  }, [room.id, room.membershipId, version]);
  useEffect(() => {
    if (!feed.current) return;
    if (preserve.current) {
      feed.current.scrollTop += feed.current.scrollHeight - preserve.current;
      preserve.current = 0;
    } else if (pinBottom.current) feed.current.scrollTop = feed.current.scrollHeight;
  }, [messages]);

  async function older() {
    const before = oldest.current;
    const signal = paging.current?.controller.signal;
    if (earlier || before === null || !signal || signal.aborted) return;
    setEarlier(true);
    try {
      const result = await api<Page>(`/rooms/${room.id}/messages?before=${before}`, 'GET', undefined, signal, room.membershipId);
      if (signal.aborted) return;
      oldest.current = result.messages[0]?.id ?? before;
      if (result.messages.length > 0) preserve.current = feed.current?.scrollHeight || 0;
      setMore(result.more);
      merge(result.messages);
      setNotice('');
    } catch (err) {
      if (!signal.aborted) showReadError(err);
    } finally {
      if (!signal.aborted) setEarlier(false);
    }
  }
  async function send(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const signal = paging.current?.controller.signal;
    if (sending || tooLong || !text.trim() || !signal || signal.aborted) return;
    const requestId = draft.requestId ?? crypto.randomUUID();
    updateDraft({text, requestId});
    setSending(true); setSendError('');
    try {
      const sent = await api<Message>(`/rooms/${room.id}/messages`, 'POST', {markdown: text, requestId}, signal, room.membershipId);
      if (signal.aborted) return;
      pinBottom.current = true;
      // 先让已有增量补拉使用原游标，以免跳过比本次发送更早的对端消息。
      merge([sent]);
      updateDraft({text: ''}); setPreview(false); onChange();
    } catch (err) {
      if (signal.aborted) return;
      if (err instanceof RequestError && err.status === 404) loseAccess();
      else {
        if (err instanceof RequestError && err.status === 409) updateDraft({text});
        setSendError(errorText(err));
      }
    } finally { if (!signal.aborted) setSending(false); }
  }
  return <section className="conversation">
    <header className="conversation-header">
      <button type="button" className="secondary mobile-back" onClick={onBack}>返回</button>
      <div className="avatar" aria-hidden="true">{first(room.name)}</div>
      <div className="grow"><h1>{room.name}</h1><p>{room.kind === 'direct' ? `私聊 · @${room.peerUsername}` : `群组 · ${room.memberCount} 位成员`}</p></div>
      {room.kind === 'group' && <button type="button" className="secondary" aria-expanded={members} onClick={() => setMembers(!members)}>成员</button>}
    </header>
    <p className="conversation-privacy">普通会话 · 消息正文由服务器处理并保存，拥有数据库或备份访问权限的人可以读取。</p>
    {members && <Members room={room} me={me} version={version} getRoomSignal={() => paging.current?.controller.signal} onChange={onChange} onAccessLost={loseAccess} />}
    <div className="message-feed" ref={feed} aria-label="聊天记录" onScroll={(e) => {
      const el = e.currentTarget;
      pinBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 96;
    }}>
      {more && <button type="button" className="secondary older" onClick={older} disabled={earlier}>载入更早的消息</button>}
      {loaded && !messages.length && <div className="quiet">对话从第一条消息开始。</div>}
      {notice && <p role="alert" className="error">{notice}</p>}
      {messages.map((m) => <article className={`message ${m.author.id === me.id ? 'mine' : ''}`} key={m.id}>
        <div className="message-meta"><strong>{m.author.displayName}</strong><time dateTime={new Date(m.sentAt).toISOString()}>{formatter.format(m.sentAt)}</time></div>
        <div className="markdown"><MessageBody body={m.markdown} /></div>
      </article>)}
    </div>
    <form className="composer" onSubmit={send}>
      <div className="compose-label"><label htmlFor="message">消息 <small>支持 Markdown</small></label>
        <button type="button" className="text-button" aria-pressed={preview} onClick={() => setPreview(!preview)}>{preview ? '返回编辑' : '预览'}</button></div>
      {preview && <div className="markdown preview" aria-label="消息预览">{text.trim() ? <MessageBody body={text}/> : '还没有内容'}</div>}
      <textarea hidden={preview} id="message" placeholder="写点什么…" value={text} disabled={sending} onChange={(e) => {updateDraft({text: e.target.value});setSendError('');}}
        onKeyDown={e => {if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && !e.nativeEvent.isComposing) {e.preventDefault();e.currentTarget.form?.requestSubmit();}}}/>
      <div className="compose-actions">{composerError && <span className="error" role="alert">{composerError}</span>}
        <button className="primary" disabled={sending || tooLong || !text.trim()}>{sending ? '发送中…' : sendError ? '重试发送' : '发送'}</button></div>
    </form>
  </section>;
}

type RoomSelection = {id: number; membershipId: string | null};

function Workspace({me, onLogout}: {me: Account; onLogout: () => void}) {
  const [{rooms, selected, roomNotice}, setRoomsView] = useState<{
    rooms: Room[]; selected?: RoomSelection; roomNotice?: string;
  }>({rooms: []});
  const [create, setCreate] = useState<Room['kind']>();
  const [version, setVersion] = useState(0);
  const [network, setNetwork] = useState('正在连接');
  const [error, setError] = useState('');
  const roomsRequest = useRef<AbortController | null>(null);
  const notify = () => setVersion(v => v + 1);
  const logoutRef = useRef(onLogout);
  logoutRef.current = onLogout;
  useEffect(() => {
    let events: EventSource | undefined;
    function connect() {
      events?.close();
      events = new EventSource('/api/v1/events');
      events.addEventListener('sync', () => {setNetwork('已连接'); notify();});
      events.onerror = () => {setNetwork('正在重连'); notify();};
    }
    function disconnect() {
      events?.close();
      setNetwork('正在重连');
    }
    function resume() {
      connect();
      notify();
    }
    if (navigator.onLine) connect();
    else disconnect();
    window.addEventListener('offline', disconnect);
    window.addEventListener('online', resume);
    // 反代缓冲或实时连接到上限时也能补拉。
    const interval = window.setInterval(notify, 12000);
    return () => {
      events?.close();
      window.clearInterval(interval);
      window.removeEventListener('offline', disconnect);
      window.removeEventListener('online', resume);
    };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    roomsRequest.current = controller;
    api<Room[]>('/rooms', 'GET', undefined, controller.signal)
      .then(result => {
        if (controller.signal.aborted) return;
        retainDrafts(me.id, result);
        setRoomsView(view => {
          const next = result.find(room => room.id === view.selected?.id);
          if (next) return {rooms: result, selected: {id: next.id, membershipId: next.membershipId}};
          if (view.selected && view.selected.membershipId !== null) {
            return {rooms: result, roomNotice: '此会话已不可访问'};
          }
          return {...view, rooms: result};
        });
        setError('');
      })
      .catch(err => {
        if (controller.signal.aborted) return;
        if (err instanceof RequestError && err.status === 401) logoutRef.current();
        else setError(errorText(err));
      });
    return () => controller.abort();
  }, [me.id, version]);
  function selectRoom(selection?: RoomSelection) {
    setRoomsView(view => ({rooms: view.rooms, selected: selection}));
  }
  function loseRoom(room: Room) {
    roomsRequest.current?.abort();
    saveDraft(draftKey(me.id, room), {text: ''});
    setRoomsView(view => {
      const rooms = view.rooms.filter(candidate => candidate.id !== room.id || candidate.membershipId !== room.membershipId);
      if (view.selected?.id === room.id && view.selected.membershipId === room.membershipId) {
        return {rooms, roomNotice: '此会话已不可访问'};
      }
      return {...view, rooms};
    });
    notify();
  }
  const current = rooms.find(r => r.id === selected?.id);
  return <main className={`workspace${current ? ' has-room' : ''}`}>
    <aside className="rail">
      <div className="rail-top"><Brand /><div className="status-line"><span className={`dot ${network === '已连接' ? 'online' : ''}`}/>{network}<span className="host">{window.location.host}</span></div></div>
      <div className="rail-controls"><h1>会话</h1><div className="row"><button className="secondary" type="button" onClick={() => setCreate('direct')}>新私聊</button><button className="secondary" type="button" onClick={() => setCreate('group')}>建群组</button></div></div>
      {create && <NewRoom key={create} kind={create} onCancel={() => setCreate(undefined)} onOpen={id => {selectRoom({id, membershipId: null});setCreate(undefined);notify();}} />}
      {error && <p role="alert" className="error rail-error">{error}</p>}
      {roomNotice && <p role="status" className="subtle rail-error">{roomNotice}</p>}
      <nav aria-label="会话列表" className="room-list">
        {rooms.length === 0 && <p className="subtle">还没有会话。从一个熟悉的用户名开始。</p>}
        {rooms.map(r => <button type="button" key={r.id} className={r.id === selected?.id ? 'room active' : 'room'} aria-current={r.id === selected?.id ? 'true' : undefined} onClick={() => {selectRoom({id: r.id, membershipId: r.membershipId});setCreate(undefined);}}>
          <span className="avatar" aria-hidden="true">{first(r.name)}</span><span className="room-text"><strong>{r.name}</strong><small>{r.kind === 'group' ? `群组 · ${r.memberCount} 人` : `@${r.peerUsername}`}</small></span>
        </button>)}
      </nav>
      <footer className="rail-foot"><div className="account-line"><div className="avatar" aria-hidden="true">{first(me.displayName)}</div><div className="grow"><strong>{me.displayName}</strong><small>@{me.username}</small></div>
        <button type="button" className="text-button" onClick={async () => {try {await api('/logout', 'POST');onLogout();} catch (err) {setError(errorText(err));}}}>退出</button></div><ServerPicker /></footer>
    </aside>
    {current ? <Conversation key={current.id + ':' + current.membershipId} room={current} me={me} version={version} onChange={notify} onBack={() => selectRoom()} onAccessLost={() => loseRoom(current)} /> :
      <section className="welcome"><span className="eyebrow">RADISHCHAT WORKSPACE</span><h2>今天聊点什么？</h2><p>打开一个会话，或用对方在这个服务器上的用户名发起私聊。</p></section>}
  </main>;
}

function App() {
  const [info, setInfo] = useState<Info>();
  const [account, setAccount] = useState<Account | null>(null);
  const [error, setError] = useState('');
  const [accessVersion, setAccessVersion] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    Promise.all([
      api<Info>('/info', 'GET', undefined, controller.signal),
      api<Account>('/me', 'GET', undefined, controller.signal).catch(err => {
        if (err instanceof RequestError && err.status === 401) {
          if (!controller.signal.aborted) clearDrafts();
          return null;
        }
        throw err;
      }),
    ]).then(([current, user]) => { if (!controller.signal.aborted) {setAccount(user);setInfo(current);} })
      .catch(err => { if (!controller.signal.aborted) setError(errorText(err)); });
    return () => controller.abort();
  }, [accessVersion]);
  function returnToAccess() {
    clearDrafts();
    setAccount(null);
    setInfo(undefined);
    setError('');
    setAccessVersion(version => version + 1);
  }
  if (!info) return <main className="initial"><Brand /><p role={error ? 'alert' : 'status'}>{error || '正在连接服务器…'}</p>{error && <button className="secondary" onClick={() => window.location.reload()}>重试</button>}</main>;
  return account ? <Workspace key={account.id} me={account} onLogout={returnToAccess} /> : <Access info={info} onLogin={setAccount} onInfo={setInfo} />;
}

window.addEventListener('pageshow', event => {
  if (event.persisted) window.location.reload();
});

createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>);
