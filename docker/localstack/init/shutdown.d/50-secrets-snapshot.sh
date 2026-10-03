#!/bin/bash
# Last export before LocalStack stops; the gateway still serves internal requests here.
exec /etc/localstack/secrets-snapshot/secrets-snapshot export --during-shutdown
