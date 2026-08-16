#!/usr/bin/env python3
# 可编程坏代理：HTTPS CONNECT 隧道，支持限速/半途掐断/拒绝/重定向。
# 模式由 PROXY_MODE 控制：
#   passthrough      正常转发
#   cut:N            双向总流量达 N 字节后同时掐断两端
#   throttle:R       限速 R 字节/秒
#   refuse           对 CONNECT 直接回 502
#   redirect         对 CONNECT 回 302 到 evil.invalid
import os, socket, threading, time, sys

PORT = int(os.environ.get("PROXY_PORT", "18080"))
MODE = os.environ.get("PROXY_MODE", "passthrough")

LOG = open(os.environ.get("PROXY_LOG",
          os.path.join(os.environ.get("STRESS", "/tmp"), "log", "proxy.log")), "a", buffering=1)

class Budget:
    def __init__(self, n):
        self.left = n
        self.lock = threading.Lock()
    def take(self, k):
        with self.lock:
            if self.left is None:
                return True
            if self.left <= 0:
                return False
            self.left -= k
            return True

def pump(src, dst, budget=None, rate=None):
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            if budget is not None and not budget.take(len(data)):
                break
            dst.sendall(data)
            if rate:
                time.sleep(len(data) / rate)
    except OSError:
        pass
    finally:
        try:
            dst.shutdown(socket.SHUT_WR)
        except OSError:
            pass

def handle(conn):
    try:
        req = b""
        while b"\r\n\r\n" not in req:
            chunk = conn.recv(4096)
            if not chunk:
                conn.close()
                return
            req += chunk
            if len(req) > 16384:
                conn.close()
                return
        line = req.split(b"\r\n")[0].decode("latin-1")
        LOG.write(f"{time.strftime('%H:%M:%S')} {line}\n")
        if b"Authorization" in req:
            LOG.write("!!! AUTHORIZATION HEADER LEAKED TO PROXY !!!\n")
        method, target, _ = line.split(" ", 2)
        if method != "CONNECT":
            conn.sendall(b"HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
            conn.close()
            return
        host, port = target.split(":")
        if MODE == "refuse":
            conn.sendall(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
            conn.close()
            return
        if MODE == "redirect":
            conn.sendall(b"HTTP/1.1 302 Found\r\nLocation: http://evil.invalid/\r\nContent-Length: 0\r\n\r\n")
            conn.close()
            return
        try:
            up = socket.create_connection((host, int(port)), timeout=15)
        except OSError:
            conn.sendall(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
            conn.close()
            return
        conn.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        budget = None
        rate = None
        if MODE.startswith("cut:"):
            budget = Budget(int(MODE.split(":")[1]))
        elif MODE.startswith("throttle:"):
            rate = int(MODE.split(":")[1])
        t1 = threading.Thread(target=pump, args=(conn, up, budget, rate), daemon=True)
        t2 = threading.Thread(target=pump, args=(up, conn, budget, rate), daemon=True)
        t1.start(); t2.start()
        t1.join(timeout=600); t2.join(timeout=600)
        conn.close()
    except Exception as e:
        LOG.write(f"proxy error: {e}\n")
        try:
            conn.close()
        except OSError:
            pass

def main():
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", PORT))
    srv.listen(64)
    LOG.write(f"proxy up: port={PORT} mode={MODE}\n")
    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle, args=(conn,), daemon=True).start()

if __name__ == "__main__":
    main()
