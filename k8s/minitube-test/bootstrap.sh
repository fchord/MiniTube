#!/usr/bin/env bash
# First-time (or re-apply) test env on the existing cluster. Does not touch ns minitube.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
NS=minitube-test
NFS_SERVER="${NFS_SERVER:-192.168.43.131}"

need() { command -v "$1" >/dev/null || { echo "missing $1"; exit 1; }; }
need kubectl

# 不要 source 仓库根 .env：那里常是生产/本机密钥。新 Secret 用 TEST_* 或随机值。
POSTGRES_PASSWORD="${TEST_POSTGRES_PASSWORD:-$(openssl rand -hex 16)}"
JWT_SECRET="${TEST_JWT_SECRET:-$(openssl rand -hex 24)}"
SRS_HOOK_SECRET="${TEST_SRS_HOOK_SECRET:-$(openssl rand -hex 16)}"

hostexec() {
  local node="$1"
  local name="$2"
  local body="$3"
  kubectl -n kube-system delete job "$name" --ignore-not-found
  cat >"/tmp/${name}.yaml" <<YML
apiVersion: batch/v1
kind: Job
metadata:
  name: ${name}
  namespace: kube-system
spec:
  ttlSecondsAfterFinished: 120
  backoffLimit: 2
  template:
    spec:
      hostNetwork: true
      hostPID: true
      restartPolicy: Never
      nodeSelector:
        kubernetes.io/hostname: ${node}
      containers:
        - name: run
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
$(echo "$body" | sed 's/^/              /')
YML
  kubectl apply -f "/tmp/${name}.yaml"
  kubectl wait -n kube-system --for=condition=complete "job/${name}" --timeout=180s
}

echo ">> nfs-prep manifest + export /data/minitube-test on worker2"
kubectl apply -f k8s/minitube/nfs-prep.yaml
hostexec k8s-worker2 minitube-test-nfs-export "$(cat <<'EOF'
set -euo pipefail
mkdir -p /data/minitube-test/uploads /data/minitube-test/srs-hls /data/minitube-test/live-hls
chmod 777 /data/minitube-test /data/minitube-test/uploads /data/minitube-test/srs-hls /data/minitube-test/live-hls
if ! grep -q '^/data/minitube-test ' /etc/exports 2>/dev/null; then
  echo '/data/minitube-test 192.168.43.0/24(rw,sync,no_subtree_check,no_root_squash)' >> /etc/exports
fi
exportfs -ra
showmount -e 127.0.0.1
grep -q '/data/minitube-test' <<< "$(showmount -e 127.0.0.1)"
EOF
)"

echo ">> namespace + quota + secret"
kubectl apply -f k8s/minitube-test/namespace.yaml
kubectl apply -f k8s/minitube-test/quota.yaml
kubectl apply -f k8s/minitube-test/limitrange.yaml
if ! kubectl -n "$NS" get secret minitube >/dev/null 2>&1; then
  POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 16)}"
  JWT_SECRET="${JWT_SECRET:-$(openssl rand -hex 24)}"
  SRS_HOOK_SECRET="${SRS_HOOK_SECRET:-$(openssl rand -hex 16)}"
  kubectl -n "$NS" create secret generic minitube \
    --from-literal=POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
    --from-literal=JWT_SECRET="$JWT_SECRET" \
    --from-literal=SRS_HOOK_SECRET="$SRS_HOOK_SECRET"
  echo "   created secret minitube (generated if .env had no keys)"
else
  echo "   keep existing secret minitube"
fi

echo ">> pvc + config + postgres + workloads"
kubectl apply -f k8s/minitube-test/pvc.yaml
kubectl apply -f k8s/minitube-test/configmap.yaml
kubectl apply -f k8s/minitube-test/postgres.yaml
kubectl apply -f k8s/minitube-test/api.yaml
kubectl apply -f k8s/minitube-test/srs.yaml
kubectl apply -f k8s/minitube-test/workers.yaml

echo ">> wait postgres"
kubectl -n "$NS" rollout status statefulset/minitube-pg --timeout=180s

echo ">> build and import API image :test"
ENV=test "$ROOT/k8s/minitube/deploy-api.sh"

echo ">> wait srs + nvenc worker (may stay Pending if GPU image missing)"
kubectl -n "$NS" rollout status deployment/minitube-srs --timeout=180s || true
kubectl -n "$NS" rollout status deployment/minitube-worker-nvenc-w2 --timeout=180s || true

echo ">> healthz"
code=$(curl -sS -m 8 -o /dev/null -w "%{http_code}" http://127.0.0.1:8081/healthz || true)
echo "local :8081 healthz: ${code}"
pub=$(curl -sS -m 12 -o /dev/null -w "%{http_code}" https://minitube-test.19121122.xyz/healthz || true)
echo "public healthz: ${pub}"
kubectl -n "$NS" get pods -o wide
if [ "$code" != "204" ]; then
  echo "expected 204 from http://127.0.0.1:8081/healthz"
  exit 1
fi
echo "bootstrap test done. site https://minitube-test.19121122.xyz  OBS rtmp://192.168.43.111:1936/live"
echo "seed admin: kubectl -n minitube-test patch secret minitube --type merge -p '{\"stringData\":{\"ADMIN_PASSWORD\":\"<口令>\"}}' && kubectl -n minitube-test rollout restart deploy/minitube-api"
