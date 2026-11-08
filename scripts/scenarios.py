# Drive the real backend through many paths, like AppLoad would (no
# DuckDuckGo traffic). Usage: python3 scenarios.py [wrapper ...] /path/to/entry
import json, os, socket, struct, subprocess, sys, time
path = "/tmp/appload.sock"
if os.path.exists(path): os.unlink(path)
srv = socket.socket(socket.AF_UNIX, socket.SOCK_SEQPACKET); srv.bind(path); srv.listen(1)
proc = subprocess.Popen(sys.argv[1:] + [path], stderr=open(os.environ.get("BACKEND_LOG", "/dev/null"), "w"))
c, _ = srv.accept(); c.settimeout(90)
def send(t, obj=None):
    b = json.dumps(obj or {}).encode(); c.send(struct.pack("<ii", t, len(b))); c.send(b)
def until(wants, limit=60):
    end = time.time() + limit
    while time.time() < end:
        try: t, n = struct.unpack("<ii", c.recv(8))
        except socket.timeout: return None, None
        body = c.recv(n).decode() if n else ""
        if t in wants: return t, (json.loads(body) if body else {})
    return None, None
def page(inp):
    send(2, {"input": inp}); t, d = until((103, 102))
    print(f"{inp[:60]:60} ->", "page " + d["page"]["title"][:40] if t == 103 else (d or {}).get("message", "timeout")[:70])
send(1, {"width": 842, "height": 1257}); print("state:", until((101,))[1]["sandbox"])
page("https://www.cyclingweekly.com/")
page("https://road.cc/")
page("https://en.wikipedia.org/wiki/Electronic_paper")
send(7, {"page": 3})
send(5); print("back:", until((103,))[0]); send(6); print("forward:", until((103,))[0])
send(9, {"greyImages": True}); print("grey:", until((103,))[0]); send(9, {"greyImages": False, "fontSize": 36, "serif": True}); until((103,))
send(13, {"width": 1500, "height": 700}); print("rotate:", until((103,))[0])
send(11); print("reload:", until((103,))[0])
page("https://no-such-host.invalid/")
page("https://expired.badssl.com/")
page("https://self-signed.badssl.com/")
page("http://neverssl.com/")
page("https://ipv6.google.com/")
page("https://192.168.1.1/")
page("https://www.rfc-editor.org/rfc/rfc2616.txt")
page("https://upload.wikimedia.org/wikipedia/commons/a/a9/Example.jpg")
page("https://httpbin.org/status/500")
page("https://example.com:9999/")
send(2, {"input": "https://httpbin.org/delay/8"}); time.sleep(1.5); send(12); print("cancel: ok"); time.sleep(1)
page("https://www.macrumors.com/")
send(10); until((107,)); print("home ok")
send(8); print("clear:", until((108, 102))[1])
time.sleep(5)  # idle: timers, GC, netpoll
page("https://www.engadget.com/")
proc.terminate(); proc.wait()
