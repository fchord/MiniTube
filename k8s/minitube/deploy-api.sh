#!/usr/bin/env bash
# Rebuild minitube-api, import into k8s-master containerd, rollout, verify healthz.
# API uses hostPort 8080/18080 on k8s-master; only that node needs the new image.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
TAR_DIR="${TAR_DIR:-/tmp/minitube-images}"
NS=minitube

need() { command -v "$1" >/dev/null || { echo "missing $1"; exit 1; }; }
need kubectl
need docker
need go

if [ "${SKIP_TEST:-}" != "1" ]; then
  echo ">> go test ./internal/httpapi"
  (cd api && go test ./internal/httpapi)
fi

echo ">> go build linux/amd64"
mkdir -p dist "$TAR_DIR"
(cd api && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../dist/minitube-api ./cmd/api)

if [ ! -d dist/ffmpeg-bundle ]; then
  echo ">> bundle ffmpeg"
  python3 k8s/minitube/bundle-ffmpeg.py
fi

echo ">> docker build"
docker build \
  --build-arg http_proxy= \
  --build-arg https_proxy= \
  --build-arg HTTP_PROXY= \
  --build-arg HTTPS_PROXY= \
  -f k8s/minitube/Dockerfile.api -t minitube/api:local .

echo ">> docker save"
docker save minitube/api:local -o "$TAR_DIR/minitube-api.tar"

echo ">> import on k8s-master"
kubectl -n kube-system delete job minitube-import-k8s-master --ignore-not-found
cat >/tmp/minitube-import-k8s-master.yaml <<YML
apiVersion: batch/v1
kind: Job
metadata:
  name: minitube-import-k8s-master
  namespace: kube-system
spec:
  ttlSecondsAfterFinished: 300
  backoffLimit: 2
  template:
    spec:
      hostNetwork: true
      hostPID: true
      restartPolicy: Never
      nodeSelector:
        kubernetes.io/hostname: k8s-master
      containers:
        - name: import
          image: debian:12-slim
          imagePullPolicy: IfNotPresent
          securityContext:
            privileged: true
          command:
            - nsenter
            - -t
            - "1"
            - -m
            - -u
            - -i
            - -n
            - --
            - bash
            - -lc
            - |
              set -euo pipefail
              ctr -n k8s.io images import ${TAR_DIR}/minitube-api.tar
YML
kubectl apply -f /tmp/minitube-import-k8s-master.yaml
kubectl wait -n kube-system --for=condition=complete job/minitube-import-k8s-master --timeout=600s

OLD=$(kubectl -n "$NS" get pods -l app=minitube-api --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
echo ">> rollout restart (old pod: ${OLD:-none})"
kubectl -n "$NS" rollout restart deploy/minitube-api

for i in $(seq 1 40); do
  if kubectl -n "$NS" rollout status deploy/minitube-api --timeout=8s; then
    break
  fi
  pend=$(kubectl -n "$NS" get pods -l app=minitube-api --no-headers 2>/dev/null | awk '$3=="Pending"{print $1; exit}')
  if [ -n "${pend:-}" ] && [ -n "${OLD:-}" ]; then
    if kubectl -n "$NS" get pod "$OLD" >/dev/null 2>&1; then
      echo ">> hostPort deadlock: delete old Running pod $OLD"
      kubectl -n "$NS" delete pod "$OLD" --wait=false || true
      OLD=""
    fi
  fi
done
kubectl -n "$NS" rollout status deploy/minitube-api --timeout=120s

echo ">> healthz"
code=$(curl -sS -m 8 -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/healthz || true)
echo "local healthz: ${code}"
if [ "$code" != "204" ]; then
  echo "expected 204 from http://127.0.0.1:8080/healthz"
  kubectl -n "$NS" get pods -l app=minitube-api -o wide
  exit 1
fi
kubectl -n "$NS" get pods -l app=minitube-api -o wide
echo "deploy-api done"
