"""Independent, one-page acceptance client for the public Bot API."""

import argparse
import json
import os
from pathlib import Path
import ssl
import sys
import tempfile
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import HTTPSHandler, HTTPRedirectHandler, Request, build_opener


class StopOnRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def run(args):
    origin = urlsplit(args.instance)
    if (origin.scheme != "https" or not origin.hostname or origin.username is not None
            or origin.path not in ("", "/") or origin.query or origin.fragment):
        raise ValueError("instance must be an HTTPS origin")
    token = args.token_file.read_text().rstrip("\r\n")
    if not token:
        raise ValueError("empty Bot token")
    headers = {"Authorization": f"Bearer {token}",
               "X-RadishChat-Bot-Installation": args.installation}
    tls = ssl.create_default_context()
    if args.ca_file:
        tls.load_verify_locations(cafile=args.ca_file)
    client = build_opener(HTTPSHandler(context=tls), StopOnRedirect())
    events_url = f"{origin.scheme}://{origin.netloc}/api/v1/bot/rooms/{args.room}/events"
    try:
        cursor = args.cursor_file.read_text()
    except FileNotFoundError:
        cursor = ""
    params = {"after": cursor} if cursor else {}
    query = "?" + urlencode(params) if params else ""
    with client.open(Request(events_url + query, headers=headers), timeout=10) as response:
        page = json.load(response)

    for event in page["events"]:
        if (event["type"], event["version"], event["payload"]["command"]) != ("command.requested", 1, "ping"):
            raise ValueError("unsupported command event")
        reply_url = f"{events_url}/{quote(event['id'], safe='')}/reply"
        reply = Request(reply_url, data=b'{"markdown":"pong"}',
                        headers={**headers, "Content-Type": "application/json"})
        with client.open(reply, timeout=10) as response:
            if response.status not in (200, 201):
                raise ValueError(f"unexpected reply status {response.status}")
            print(f"reply {response.status}")
    if page["events"]:
        # The operator assigns a different file to each origin and installation.
        with tempfile.NamedTemporaryFile(mode="w", dir=args.cursor_file.parent, delete=False) as tmp:
            path = Path(tmp.name)
            try:
                tmp.write(page["nextCursor"])
                tmp.close()
                os.replace(path, args.cursor_file)
            finally:
                path.unlink(missing_ok=True)
    print(f"events {len(page['events'])}, hasMore {page['hasMore']}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--instance", required=True)
    parser.add_argument("--room", type=int, required=True)
    parser.add_argument("--installation", required=True)
    parser.add_argument("--token-file", type=Path, required=True)
    parser.add_argument("--cursor-file", type=Path, required=True)
    parser.add_argument("--ca-file", type=Path)
    try:
        run(parser.parse_args())
    except HTTPError as error:
        sys.exit(f"HTTP {error.code}")
    except (OSError, URLError, ValueError, KeyError) as error:
        sys.exit(str(error))
