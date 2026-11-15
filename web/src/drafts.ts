import type {Room} from './api';

export type Draft = {text: string; requestId?: string};

const namespace = 'radishchat.draft.';
const accountPrefix = (accountId: number) => `${namespace}${accountId}:`;
export const draftKey = (accountId: number, room: Room) =>
  `${accountPrefix(accountId)}${room.id}:${room.membershipId}`;

export function readDraft(key: string): Draft {
  try {
    const saved = JSON.parse(sessionStorage.getItem(key) ?? 'null');
    if (saved && typeof saved.text === 'string') {
      const {text, requestId} = saved;
      return typeof requestId === 'string' ? {text, requestId} : {text};
    }
  } catch {
    // 浏览器禁用存储或草稿损坏时，仍可在当前页面编辑。
  }
  return {text: ''};
}

export function saveDraft(key: string, draft: Draft) {
  try {
    if (draft.text) sessionStorage.setItem(key, JSON.stringify(draft));
    else sessionStorage.removeItem(key);
  } catch {
    // 存储不可用时，本页的 React 状态仍保留草稿。
  }
}

function pruneDrafts(prefix: string, keep: ReadonlySet<string>) {
  try {
    for (let i = sessionStorage.length - 1; i >= 0; i--) {
      const key = sessionStorage.key(i);
      if (key?.startsWith(prefix) && !keep.has(key)) sessionStorage.removeItem(key);
    }
  } catch {
    // 存储不可用时，后续页面也无法读取这些键。
  }
}

export function retainDrafts(accountId: number, rooms: Room[]) {
  pruneDrafts(accountPrefix(accountId), new Set(rooms.map(room => draftKey(accountId, room))));
}

export function clearDrafts() {
  pruneDrafts(namespace, new Set());
}
