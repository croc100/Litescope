import sqlite3, os, time, shutil, glob

os.chdir(os.path.dirname(os.path.abspath(__file__)))
# clean
for p in glob.glob("*.db")+glob.glob("*.db-*")+["litescope.fleet.yaml","schema.sql"]:
    try: os.remove(p)
    except: pass

# ── Scene 1/2: a single AI-app database with a classic FK-no-index bug ──
c=sqlite3.connect("app.db")
c.executescript("""
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT);
CREATE TABLE messages (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), body TEXT);
CREATE TABLE embeddings (id INTEGER PRIMARY KEY, doc TEXT, vector TEXT);
""")
for i in range(500): c.execute("INSERT INTO users(name,email) VALUES(?,?)",(f"u{i}",f"u{i}@x.ai"))
c.commit(); c.close()

# desired schema (declarative) — adds a column + an index
open("schema.sql","w").write("""CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, verified_at TEXT);
CREATE TABLE messages (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), body TEXT);
CREATE INDEX idx_messages_user_id ON messages(user_id);
CREATE TABLE embeddings (id INTEGER PRIMARY KEY, doc TEXT, vector TEXT);
""")

# ── Scene 3: a fleet of tenant databases ──
def canonical(path):
    c=sqlite3.connect(path)
    c.executescript("""
    CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT);
    CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), total INTEGER);
    CREATE TABLE audit_logs (id INTEGER PRIMARY KEY, action TEXT, at TEXT);
    CREATE INDEX idx_orders_user_id ON orders(user_id);
    """); c.commit(); c.close()

tenants=[]
# 5 canonical
for i in range(1,6):
    p=f"tenant-{i:04d}.db"; canonical(p); tenants.append((f"tenant-{i:04d}",p))
# 2 drifted: missing audit_logs (a migration that never reached them)
for i in range(6,8):
    p=f"tenant-{i:04d}.db"; c=sqlite3.connect(p)
    c.executescript("""
    CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT);
    CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), total INTEGER);
    CREATE INDEX idx_orders_user_id ON orders(user_id);
    """); c.commit(); c.close(); tenants.append((f"tenant-{i:04d}",p))
# 1 corrupt — with a healthy backup alongside (for recover)
canonical("tenant-0008.db")
ts=time.strftime("%Y%m%d-%H%M%S")
shutil.copy("tenant-0008.db", f"tenant-0008.backup-{ts}.db")     # healthy backup
open("tenant-0008.db","wb").write(b"SQLite format 3\x00 CORRUPT not a real database body")
tenants.append(("tenant-0008","tenant-0008.db"))

# fleet config — all in group:prod (so blast-radius shows the cohort)
lines=["version: 1","name: production","databases:"]
for name,p in tenants:
    lines+=[f"  - name: {name}",f"    dsn: {p}","    tags: [group:prod]"]
open("litescope.fleet.yaml","w").write("\n".join(lines)+"\n")
print(f"demo ready: app.db + {len(tenants)} tenants (5 canonical, 2 drifted, 1 corrupt+backup)")
