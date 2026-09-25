#!/usr/bin/env bash
# Rebuild minitube-api, import into k8s-master containerd, rollout, verify healthz.
# ENV=prod|test is required (no default). Prod: ns minitube hostPort 8080 image :local.
# Test: ns minitube-test hostPort 8081 image :test.
#
# Import reads TAR_DIR/minitube-api.tar on the k8s-master HOST (nsenter).
# Run this on k8s-master, or a machine that shares that tar path with master.
# Building on another machine? Use build-images.sh HTTP import instead.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
TAR_DIR="${TAR_DIR:-/tmp/minitube-images}"

case "${ENV:-}" in
  prod)
    NS=minitube
    IMAGE_TAG=local
    HEALTHZ_URL=http://127.0.0.1:8080/healthz
    PUBLIC_HEALTHZ_URL=https://minitube.19121122.xyz/healthz
    ;;
  test)
    NS=minitube-test
    IMAGE_TAG=test
    HEALTHZ_URL=http://127.0.0.1:8081/healthz
    PUBLIC_HEALTHZ_URL=https://minitube-test.19121122.xyz/healthz
    ;;
  *)
    echo "ENV=prod|test required (refusing to guess so prod is not overwritten)"
    exit 1
    ;;
esac

need() { command -v "$1" >/dev/null || { echo "missing $1"; exit 1; }; }
need kubectl
need docker
need go

if ! kubectl -n "$NS" get deploy minitube-api >/dev/null 2>&1; then
  echo "namespace $NS has no deploy/minitube-api"
  if [ "$ENV" = "test" ]; then
    echo "first time: ./k8s/minitube-test/bootstrap.sh"
  fi
  exit 1
fi

if [ "${SKIP_TEST:-}" != "1" ]; then
  echo ">> go test ./internal/httpapi"
  (cd api && go test ./internal/httpapi)
fi

echo ">> go build linux/amd64 (ENV=$ENV image=minitube/api:$IMAGE_TAG)"
mkdir -p dist "$TAR_DIR"
(cd api && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../dist/minitube-api ./cmd/api)

if [ ! -d dist/ffmpeg-bundle ]; then
  echo ">> bundle ffmpeg"
  python3 k8s/minitube/bundle-ffmpeg.py
fi

echo ">> docker build minitube/api:$IMAGE_TAG"
docker build \
  --build-arg http_proxy= \
  --build-arg https_proxy= \
  --build-arg HTTP_PROXY= \
  --build-arg HTTPS_PROXY= \
  -f k8s/minitube/Dockerfile.api -t "minitube/api:${IMAGE_TAG}" .

echo ">> docker save"
docker save "minitube/api:${IMAGE_TAG}" -o "$TAR_DIR/minitube-api.tar"

echo ">> import on k8s-master host path ${TAR_DIR}/minitube-api.tar"
echo "   (run on k8s-master or a host sharing that path; otherwise use build-images.sh HTTP import)"
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

OLD=$(kubectl -n "$NS" get pods -l app=minitube-api -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
echo ">> rollout restart $NS/minitube-api (old pod: ${OLD:-none})"
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
code=$(curl -sS -m 8 -o /dev/null -w "%{http_code}" "$HEALTHZ_URL" || true)
echo "local healthz: ${code} ($HEALTHZ_URL)"
if [ "$code" != "204" ]; then
  echo "expected 204 from $HEALTHZ_URL"
  kubectl -n "$NS" get pods -l app=minitube-api -o wide
  exit 1
fi
pub=$(curl -sS -m 12 -o /dev/null -w "%{http_code}" "$PUBLIC_HEALTHZ_URL" || true)
echo "public healthz: ${pub} ($PUBLIC_HEALTHZ_URL)"
kubectl -n "$NS" get pods -l app=minitube-api -o wide
echo "deploy-api ENV=$ENV done"
