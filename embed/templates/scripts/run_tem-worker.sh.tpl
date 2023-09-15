#!/bin/bash
set -e

# WARNING: This file was auto-generated. Do not edit!
#          All your edit might be overwritten!
DEPLOY_DIR={{.DeployDir}}

cd "${DEPLOY_DIR}" || exit 1

{{- if .NumaNode}}
exec numactl --cpunodebind={{.NumaNode}} --membind={{.NumaNode}} bin/tem-worker/tem-worker \
{{- else}}
exec bin/tem-worker/tem-worker \
{{- end}}
    --log-file="{{.LogDir}}/tem-worker.log" \
    --html=etc/html \
    --config=etc/config/example.conf >> "{{.LogDir}}/temworker_stdout.log" 2>> "{{.LogDir}}/tem-worker_stderr.log"
