#!/usr/bin/env python3
"""Exercise the production Nginx template against a temporary HTTP upstream."""
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True)


class Upstream(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(503)
        self.end_headers()
        self.wfile.write(b"unavailable\n")

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"path": self.path, "body": body.decode(),
                                    "accept": self.headers.get("Accept"),
                                    "protocol": self.headers.get("MCP-Protocol-Version")}).encode())

    def log_message(self, *_):
        pass


def main():
    env = dict(os.environ, SEARXNG_SECRET="proxy-config-test")
    config = json.loads(subprocess.check_output(
        ["docker", "compose", "--profile", "proxy", "config", "--format", "json"],
        cwd=ROOT, env=env, text=True))
    proxy = config["services"]["nginx"]
    assert proxy["network_mode"] == "host" and not proxy.get("ports")
    binding = config["services"]["search-mcp"]["ports"][0]
    assert binding["host_ip"] == "127.0.0.1"
    upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    name = "search-mcp-proxy-test-" + uuid.uuid4().hex[:12]
    args = ["docker", "run", "--rm", "--network", "host",
            "-e", f"NGINX_PORT={port}", "-e", f"MCP_PORT={upstream.server_port}",
            "-v", f"{ROOT / 'deploy/nginx/default.conf.template'}:/etc/nginx/templates/default.conf.template:ro"]
    try:
        print(run(*args, proxy["image"], "nginx", "-t").strip())
        run(*args, "--name", name, "-d", proxy["image"])
        base = f"http://127.0.0.1:{port}"
        for _ in range(100):
            try:
                urllib.request.urlopen(base + "/ready", timeout=1)
            except urllib.error.HTTPError as error:
                assert error.code == 503 and error.read() == b"unavailable\n"
                break
            except urllib.error.URLError:
                time.sleep(0.1)
        else:
            raise AssertionError("proxy did not become ready")
        body = '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
        req = urllib.request.Request(base + "/mcp?test=1", data=body.encode(), headers={
            "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": "2025-03-26"})
        with urllib.request.urlopen(req, timeout=5) as response:
            result = json.load(response)
        assert result == {"path": "/mcp?test=1", "body": body,
                          "accept": "application/json, text/event-stream", "protocol": "2025-03-26"}, result
        for path in ("/", "/mcp/", "/unknown"):
            try:
                urllib.request.urlopen(base + path, timeout=5)
            except urllib.error.HTTPError as error:
                assert error.code == 404
            else:
                raise AssertionError(f"unexpected route: {path}")
        print("PASS: forwarding, protocol headers, upstream status, route isolation")
    finally:
        subprocess.run(["docker", "rm", "-f", name], capture_output=True)
        upstream.shutdown()
        upstream.server_close()


if __name__ == "__main__":
    main()
