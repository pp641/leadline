"""Fails if a route registered in the Go code is missing from docs/openapi.yaml (or vice versa)."""
import glob, re, sys
import yaml

spec = yaml.safe_load(open("docs/openapi.yaml"))["paths"]
norm = lambda p: re.sub(r"\{[^}]+\}", "{}", p)
documented = {(m.upper(), norm(p)) for p, ops in spec.items() for m in ops if m in ("get", "post", "put", "patch", "delete")}
routes = set()
for f in glob.glob("backend/internal/**/*.go", recursive=True):
    for m, p in re.findall(r'"(GET|POST|PUT|PATCH|DELETE) (/api/v1[^"]*)"', open(f).read()):
        routes.add((m, norm(p[len("/api/v1"):])))
missing, extra = sorted(routes - documented), sorted(documented - routes)
print(f"{len(routes)} routes registered, {len(documented)} documented")
for m in missing:
    print("undocumented:", *m)
for e in extra:
    print("documented but not registered:", *e)
sys.exit(1 if missing or extra else 0)
