#!/usr/bin/env python3
"""Inventory Xip reference files without copying or executing their code.

The manifest separates feature files from the static local import closure.
Dependencies are references to inspect, not instructions to transplant entire
packages into Macro. Only tracked files are considered; .env is never read.
"""

import argparse
import hashlib
import json
import re
import subprocess
from collections import Counter, deque
from datetime import datetime, timezone
from pathlib import Path


FEATURES = ("kyc", "customers", "integration", "ramps", "bank", "pixkeys", "companyregistry")
ADMIN_FEATURES = ("kyc", "customers", "providers", "ramps", "bank", "webhooks")
TOPIC = re.compile(r"ramp|kyc|kyb|provider|customer|beneficiar|offramp|liquidation|pix|usd", re.I)
GO_IMPORT = re.compile(r'"(xip-backend/[^"\s]+)"')
TS_IMPORT = re.compile(r'''(?:from\s*|import\s*\(\s*|import\s*)["']@/([^"']+)["']''')


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


def tracked(root):
    raw = subprocess.check_output(["git", "-C", str(root), "ls-files", "-z"])
    return sorted(set(p for p in raw.decode().split("\0") if p and (root / p).is_file()))


def in_feature(path, prefix, names):
    return any(path.startswith(f"{prefix}/{name}/") for name in names)


def backend_category(path, root):
    if path.startswith("app/adapters/ramp/"):
        return "adapter_and_contract"
    for audience, names in (("client", FEATURES), ("admin", ADMIN_FEATURES)):
        if in_feature(path, f"app/services/{audience}", names):
            return "domain_service"
        for layer in ("controllers", "requests", "resources"):
            if in_feature(path, f"app/http/{layer}/api/{audience}", names):
                return f"http_{audience}"
        if in_feature(path, f"app/dto/api/{audience}", names):
            return "dto"
        if in_feature(path, f"tests/feature/api/{audience}", names):
            return "http_feature_test"
    if path.startswith("app/services/ramp/"):
        return "lifecycle_and_recovery"
    if in_feature(path, "app/featureflags", ("ramp", "kyc", "customers")):
        return "feature_flag"
    if path.startswith("app/settings/"):
        return "settings_reference"
    if path.startswith("database/migrations/") and path.endswith(".go"):
        if TOPIC.search(path) or TOPIC.search((root / path).read_text()):
            return "schema_lineage_reference"
    if path.startswith("app/models/") and TOPIC.search(path):
        return "model_reference"
    if path.startswith("app/repositories/") and TOPIC.search(path):
        return "repository_reference"
    if path.startswith("app/http/controllers/webhooks/"):
        return "webhook_ingress"
    if path.startswith("app/console/commands/") and TOPIC.search(path):
        return "operations_command"
    if path.startswith("app/jobs/") and re.search(r"kyc|ramp|offramp", path):
        return "async_job"
    if path.startswith("app/providers/") and TOPIC.search(path):
        return "composition_reference"
    if path.startswith("app/http/middleware/") and re.search(r"customer|client_ip", path):
        return "audience_scope_reference"
    if path in ("routes/api.go", "routes/admin.go", "routes/webhooks.go", "routes/web.go",
                "bootstrap/jobs.go", "bootstrap/migrations.go", "bootstrap/app.go",
                "config/ramp.go", "config/queue.go", ".env.example", "go.mod", "go.sum",
                ".mockery.yaml", "LICENSE"):
        return "wiring_or_dependency_reference"
    if path.startswith("docs/") and TOPIC.search(path):
        return "design_reference"
    return None


def frontend_category(path, _root):
    if re.match(r"lib/(customers|providers|kyc-profile|bank-beneficiaries|settings|features|webhooks|transactions)/", path):
        return "frontend_domain"
    if re.match(r"app/\(dashboard\)/(customers|kyc|settings|features|webhooks|transactions|ramps)/", path):
        return "admin_view"
    if path.startswith("app/(dashboard)/users/") and re.search(r"kyc|compliance|provider|bank|virtual", path):
        return "legacy_individual_reference"
    if path.startswith("locales/"):
        return "translation_reference"
    if path.startswith("e2e/") and TOPIC.search(path):
        return "frontend_e2e"
    if path.startswith("docs/") and TOPIC.search(path):
        return "design_reference"
    if path in ("package.json", "package-lock.json", "proxy.ts", "CLAUDE.md", ".env.example"):
        return "frontend_boundary_reference"
    return None


def inventory(root, category, language):
    files = tracked(root)
    file_set = set(files)
    chosen = {p: category(p, root) for p in files}
    chosen = {p: c for p, c in chosen.items() if c}
    pending = deque(chosen)
    scanned = set()
    package_files = {}
    for path in files:
        if path.endswith(".go"):
            package_files.setdefault(str(Path(path).parent), []).append(path)
    edges = set()
    while pending:
        path = pending.popleft()
        if path in scanned:
            continue
        scanned.add(path)
        if language == "go" and path.endswith(".go"):
            imports = GO_IMPORT.findall((root / path).read_text())
            resolutions = [(name, package_files.get(name.removeprefix("xip-backend/"), [])) for name in imports]
        elif language == "ts" and path.endswith((".ts", ".tsx", ".js", ".jsx")):
            imports = TS_IMPORT.findall((root / path).read_text())
            resolutions = []
            for name in imports:
                candidates = [name, *(name + ext for ext in (".ts", ".tsx", ".js", ".jsx", ".json")),
                              *(name + "/index" + ext for ext in (".ts", ".tsx", ".js", ".jsx"))]
                resolved = next((p for p in candidates if p in file_set), None)
                resolutions.append(("@/" + name, [resolved] if resolved else []))
        else:
            continue
        for name, destinations in resolutions:
            if destinations:
                edges.add((str(Path(path).parent), name))
            for destination in destinations:
                if destination not in chosen:
                    chosen[destination] = "static_import_dependency"
                    pending.append(destination)
    records = []
    for path, kind in sorted(chosen.items()):
        if Path(path).name.startswith(".env") and path != ".env.example":
            raise ValueError("Refusing to inventory an environment secret file")
        body = (root / path).read_bytes()
        records.append({"path": path, "category": kind, "status": "reference_only",
                        "sha256": hashlib.sha256(body).hexdigest(), "bytes": len(body),
                        "is_test": path.endswith("_test.go") or ".test." in path or path.startswith(("tests/", "e2e/"))})
    refs = {}
    for ref in ("HEAD", "origin/dev", "MERGE_HEAD"):
        result = subprocess.run(["git", "-C", str(root), "rev-parse", "--verify", ref],
                                text=True, capture_output=True)
        if result.returncode == 0:
            refs[ref] = result.stdout.strip()
    return {"root": str(root), "refs": refs,
            "has_uncommitted_tracked_changes": bool(git(root, "status", "--porcelain", "--untracked-files=no")),
            "counts": dict(sorted(Counter(r["category"] for r in records).items())),
            "file_count": len(records), "test_file_count": sum(r["is_test"] for r in records),
            "local_import_edges": [{"from_directory": a, "import": b} for a, b in sorted(edges)],
            "files": records}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--xip-backend", type=Path, required=True)
    parser.add_argument("--xip-admin", type=Path, required=True)
    parser.add_argument("--xip-admin-blindpay", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    manifest = {"schema_version": 1,
                "generated_at": datetime.now(timezone.utc).isoformat(),
                "status": "planning_reference_only_no_runtime_integration",
                "coverage": "Feature directories plus static local Go and @/ TypeScript import closure, including tests. Dynamic imports, external packages and non-code references require review.",
                "requirements": {"tenant_own_kyb_and_ramps": True, "tenant_end_customer_kyc_kyb_and_ramps": True,
                                 "end_customer_ui": "tenant_interface_via_macro_external_api",
                                 "providers": ["blindpay", "bridge", "ozav"],
                                 "directions": ["onramp", "offramp"]},
                "sources": {"xip_backend_composed": inventory(args.xip_backend.resolve(), backend_category, "go"),
                            "xip_admin_dev": inventory(args.xip_admin.resolve(), frontend_category, "ts"),
                            "xip_admin_blindpay": inventory(args.xip_admin_blindpay.resolve(), frontend_category, "ts")}}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    for name, source in manifest["sources"].items():
        print(f"{name}: {source['file_count']} reference files, {source['test_file_count']} test files")
    print(f"Wrote {args.output}")


if __name__ == "__main__":
    main()
