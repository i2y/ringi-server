#!/bin/sh
# Shows the built site on this computer, under the path it is published at, with nothing cached.
#
#   website/serve.sh [port]     # 8004 when not given
cd "$(dirname "$0")"
exec python3 - "${1:-8004}" <<'PY'
import functools, http.server, socketserver, sys

BASE = "/ringi-server"


class Site(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        super().end_headers()

    def send_head(self):
        if not self.path.startswith(BASE + "/"):
            self.send_response(302)
            self.send_header("Location", BASE + "/")
            self.end_headers()
            return None
        return super().send_head()

    def translate_path(self, path):
        return super().translate_path(path[len(BASE) :])

    def log_message(self, *a):
        pass


socketserver.ThreadingTCPServer.allow_reuse_address = True
with socketserver.ThreadingTCPServer(("127.0.0.1", int(sys.argv[1])), functools.partial(Site, directory="build")) as httpd:
    print(f"http://127.0.0.1:{sys.argv[1]}{BASE}/", flush=True)
    httpd.serve_forever()
PY
