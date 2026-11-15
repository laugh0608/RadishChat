export type Account = {id: number; username: string; displayName: string};
export type Room = {id: number; kind: 'direct' | 'group'; name: string; peerUsername?: string; ownerId?: number; memberCount: number; lastMessageId: number; membershipId: string};
type BotAuthor = {kind: 'bot'; id: string; displayName: string};
export type Message = {id: number; author: Account | BotAuthor; markdown: string; sentAt: number};
export type Page = {messages: Message[]; more: boolean};
export type Info = {registration: boolean; registrationInviteRequired?: boolean; radishLogin: boolean};

export class RequestError extends Error {
  constructor(readonly status: number, message: string) { super(message); }
}

export async function api<T>(path: string, method = 'GET', data?: unknown, signal?: AbortSignal, membershipId?: string): Promise<T> {
  const headers = new Headers();
  if (data !== undefined) headers.set('Content-Type', 'application/json');
  if (membershipId) headers.set('X-RadishChat-Membership', membershipId);
  const result = await fetch(`/api/v1${path}`, {
    method, signal, headers, cache: 'no-store', credentials: 'same-origin',
    body: data === undefined ? undefined : JSON.stringify(data),
  });
  if (!result.ok) {
    const body = await result.json().catch(() => ({}));
    throw new RequestError(result.status, body.error || '暂时无法完成请求');
  }
  return result.status === 204 ? undefined as T : await result.json() as T;
}

export function errorText(error: unknown): string {
  return error instanceof RequestError ? error.message : '无法连接服务器，请检查网络后重试。';
}

export function openServer(value: string) {
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    throw new Error('请输入完整的服务器地址，例如 https://chat.example.com');
  }
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  if ((url.protocol !== 'https:' && !(loopback && url.protocol === 'http:')) || url.username || url.password ||
      url.pathname !== '/' || url.search || url.hash) {
    throw new Error('请输入 HTTPS 服务器的根地址；本机回环地址可使用 HTTP。');
  }
  window.location.assign(url.origin);
}
