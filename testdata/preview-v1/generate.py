"""Generate and verify a disposable SQLite v1 fixture through the frozen preview API."""

import argparse
from contextlib import closing, contextmanager
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request


PASSWORD = 'fixture-only-password'
USERS = [('alice', 'Alice'), ('bob', 'Bob'), ('carol', 'Carol')]
MESSAGES = [
    ('direct', 'alice', 'preview-direct-01', '私聊\n\n- 合成样本'),
    ('group', 'alice', 'preview-group-before', '首次加入前'),
    ('group', 'carol', 'preview-group-first', '第一次在群'),
    ('group', 'bob', 'preview-group-gap', '离群期间'),
    ('group', 'carol', 'preview-group-rejoin', '重新加入后'),
]


class Client:
    def __init__(self, origin):
        self.origin = origin
        self.http = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )

    def request(self, path, payload=None, *, method='GET', expected=200):
        body = None if payload is None else json.dumps(payload).encode('utf-8')
        request = urllib.request.Request(
            self.origin + path, body, method=method,
            headers={'Origin': self.origin, 'Content-Type': 'application/json'},
        )
        with self.http.open(request, timeout=10) as result:
            if result.status != expected:
                raise RuntimeError(f'{method} {path} returned {result.status}, expected {expected}')
            return None if expected == 204 else json.load(result)


@contextmanager
def preview(binary, database):
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        address = f'127.0.0.1:{listener.getsockname()[1]}'
    origin = f'http://{address}'
    env = {
        'RADISHCHAT_LISTEN': address,
        'RADISHCHAT_PUBLIC_URL': origin,
        'RADISHCHAT_DATABASE': str(database),
        'RADISHCHAT_WEB_DIR': str(database.parent / 'unused-web'),
        'RADISHCHAT_REGISTRATION': 'on',
    }
    with tempfile.TemporaryFile() as log, subprocess.Popen(
        [str(binary)], cwd=database.parent, env=env, stdout=log, stderr=log,
    ) as server:
        try:
            deadline = time.monotonic() + 10
            while True:
                if server.poll() is not None or time.monotonic() >= deadline:
                    log.seek(0)
                    raise RuntimeError(log.read().decode(errors='replace'))
                try:
                    Client(origin).request('/healthz')
                except OSError:
                    time.sleep(0.05)
                else:
                    break
            yield origin
        finally:
            if server.poll() is None:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()
                    raise
        if server.returncode != 0:
            log.seek(0)
            raise RuntimeError(log.read().decode(errors='replace'))


def seed(origin):
    clients = {username: Client(origin) for username, _ in USERS}
    for username, display_name in USERS:
        clients[username].request('/api/v1/register', {
            'username': username, 'displayName': display_name, 'password': PASSWORD,
        }, method='POST', expected=201)

    alice, carol = clients['alice'], clients['carol']
    rooms = {
        'direct': alice.request('/api/v1/rooms/direct', {'username': 'bob'}, method='POST')['id'],
        'group': alice.request('/api/v1/rooms/group', {'name': 'Preview v1', 'usernames': ['bob']},
                               method='POST', expected=201)['id'],
    }
    group = f'/api/v1/rooms/{rooms["group"]}'
    for kind, author, request_id, body in MESSAGES:
        if request_id in ('preview-group-first', 'preview-group-rejoin'):
            alice.request(group + '/members', {'username': 'carol'}, method='POST', expected=204)
        if request_id == 'preview-group-gap':
            carol.request(group + '/leave', method='POST', expected=204)
        clients[author].request(f'/api/v1/rooms/{rooms[kind]}/messages', {
            'requestId': request_id, 'markdown': body,
        }, method='POST', expected=201)


def verify(database):
    with closing(sqlite3.connect(database.resolve().as_uri() + '?mode=ro', uri=True)) as db:
        def rows(query):
            return db.execute(query).fetchall()

        checks = {
            'user_version': (rows('PRAGMA user_version'), [(1,)]),
            'integrity': (rows('PRAGMA integrity_check'), [('ok',)]),
            'foreign_keys': (rows('PRAGMA foreign_key_check'), []),
            'users': (rows('SELECT username, display_name FROM users ORDER BY id'), USERS),
            'rooms': (rows('SELECT kind, name, owner_id, direct_a, direct_b FROM rooms ORDER BY id'),
                      [('direct', '', None, 1, 2), ('group', 'Preview v1', 1, None, None)]),
            'members': (rows('SELECT room_id, user_id FROM members ORDER BY room_id, user_id'),
                        [(1, 1), (1, 2), (2, 1), (2, 2), (2, 3)]),
            'messages': (rows('''SELECT r.kind, u.username, m.request_id, m.body
                FROM messages m JOIN rooms r ON r.id = m.room_id
                JOIN users u ON u.id = m.sender_id ORDER BY m.id'''), MESSAGES),
            'sessions': (rows('SELECT user_id, length(token_hash) FROM sessions ORDER BY user_id'),
                         [(1, 32), (2, 32), (3, 32)]),
            'external_accounts': (rows('SELECT COUNT(*) FROM external_accounts'), [(0,)]),
        }
        for name, (actual, expected) in checks.items():
            if actual != expected:
                raise ValueError(f'{name}: {actual!r} != {expected!r}')

        snapshot = {name: actual for name, (actual, _) in checks.items()}
        snapshot['schema'] = rows('SELECT type, name, sql FROM sqlite_schema ORDER BY type, name')
        snapshot['user_ids'] = rows('SELECT id, username FROM users ORDER BY id')
        snapshot['room_ids'] = rows('SELECT id, kind FROM rooms ORDER BY id')
        snapshot['message_ids'] = rows('SELECT id, request_id FROM messages ORDER BY id')
        payload = json.dumps(snapshot, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
        print('SQLite user_version=1; 3 accounts, 2 rooms, 5 membership rows, 5 messages, 3 synthetic sessions')
        print('Logical SHA-256:', hashlib.sha256(payload.encode('utf-8')).hexdigest())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('database', type=Path)
    parser.add_argument('--binary', type=Path, help='frozen preview binary; omit to verify only')
    args = parser.parse_args()
    database = args.database.resolve()
    if args.binary:
        if database.exists():
            parser.error('generation requires a new database path')
        database.parent.mkdir(parents=True, exist_ok=True)
        with preview(args.binary.resolve(), database) as origin:
            seed(origin)
    verify(database)


if __name__ == '__main__':
    main()
