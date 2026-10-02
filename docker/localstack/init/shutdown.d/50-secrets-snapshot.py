# Final encrypted Secrets Manager snapshot on graceful shutdown (docker stop / restart).
# LocalStack exec()s .py init scripts inside its own process; the gateway already answers 503
# at this stage, so the export reads the moto backend directly.
import importlib.util

SNAPSHOT_TOOL = "/etc/localstack/secrets-snapshot/secrets_snapshot.py"

spec = importlib.util.spec_from_file_location("secrets_snapshot", SNAPSHOT_TOOL)
secrets_snapshot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(secrets_snapshot)
secrets_snapshot.export_in_process()
