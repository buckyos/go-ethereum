"""Transparent loopback RPC gate for real-service delay acceptance tests."""

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import socket
import threading
import time
from urllib import request, error


class BlockDelayProxy:
    """Delay selected Core block reads; never synthesize successful RPC data.

    Historical reads and readiness polling continue on separate connections.
    Holding a raw block fetch models a slow sync worker while the real service
    still answers committed-height queries from its own database.
    """

    def __init__(self, port, upstream, output):
        self.upstream = upstream
        self.lock = threading.Lock()
        self.release = threading.Event()
        self.release.set()
        self.hashes = set()
        self.events = []
        self.stream = output.open("w")
        proxy = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def setup(self):
                super().setup()
                self.connection.settimeout(5)
                self.connection.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)

            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                calls = json.loads(body)
                calls = calls if isinstance(calls, list) else [calls]
                with proxy.lock:
                    held = [call for call in calls if call.get("method") == "getblock"
                            and call.get("params", [None])[0] in proxy.hashes]
                    if held:
                        event = {"kind": "held", "calls": held, "time": time.time()}
                        proxy.events.append(event)
                        proxy.stream.write(json.dumps(event) + "\n")
                        proxy.stream.flush()
                if held and not proxy.release.wait(timeout=90):
                    self.send_error(504, "test block delay exceeded its safety bound")
                    return
                headers = {"Content-Type": "application/json"}
                if self.headers.get("Authorization"):
                    headers["Authorization"] = self.headers["Authorization"]
                try:
                    response = request.urlopen(request.Request(proxy.upstream, data=body, headers=headers), timeout=10)
                except error.HTTPError as failure:
                    response = failure  # Core returns JSON RPC errors with HTTP 500.
                except OSError:
                    self.send_error(502, "test upstream transport unavailable")
                    return
                with response:
                    result, status = response.read(), response.code
                try:
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(result)))
                    self.end_headers()
                    self.wfile.write(result)
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def log_message(self, *args):
                pass

        class Server(ThreadingHTTPServer):
            request_queue_size = 128
            daemon_threads = False

        self.server = Server(("127.0.0.1", port), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def hold(self, block_hashes):
        with self.lock:
            if not self.release.is_set():
                raise ValueError("block delay already active")
            self.hashes = set(block_hashes)
            self.events.clear()
            self.release.clear()

    def restore(self):
        with self.lock:
            self.hashes.clear()
            self.release.set()

    def close(self):
        self.restore()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=12)
        self.stream.close()
