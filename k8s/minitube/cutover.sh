#!/usr/bin/env bash
# Migrate MiniTube onto the existing k8s cluster: NFS media, Postgres, API, SRS, GPU workers.
# Host mount / ctr import run via privileged nsenter Jobs (no sudo).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
NS=minitube
MASTER_IP="${MASTER_IP:-192.168.43.111}"
NFS_SERVER="${NFS_SERVER:-192.168.43.131}"

need() { command -v "$1" >/dev/null || { echo "missing $1"; exit 1; }; }
need kubectl
need docker

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-minitube}"
JWT_SECRET="${JWT_SECRET:-dev-change-me}"
SRS_HOOK_SECRET="${SRS_HOOK_SECRET:-dev-srs-hook}"

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
  kubectl wait -n kube-system --for=condition=complete "job/${name}" --timeout=600s
}

echo ">> namespace + secret"
kubectl apply -f k8s/minitube/namespace.yaml
kubectl -n "$NS" delete secret minitube --ignore-not-found
kubectl -n "$NS" create secret generic minitube \
  --from-literal=POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
  --from-literal=JWT_SECRET="$JWT_SECRET" \
  --from-literal=SRS_HOOK_SECRET="$SRS_HOOK_SECRET"

echo ">> NFS export on worker2 + nfs-common on nodes"
kubectl apply -f k8s/minitube/nfs-prep.yaml
echo "   waiting for nfs-prep on k8s-worker2..."
ready=0
for i in $(seq 1 60); do
  pod=$(kubectl -n "$NS" get pods -l app=minitube-nfs-prep --field-selector spec.nodeName=k8s-worker2 -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [ -n "${pod:-}" ]; then
    if kubectl -n "$NS" logs "$pod" 2>/dev/null | grep -q "nfs-prep ready"; then
      ready=1
      break
    fi
  fi
  sleep 5
done
if [ "$ready" != 1 ]; then
  echo "nfs-prep on worker2 did not report ready; dumping pods"
  kubectl -n "$NS" get pods -o wide
  kubectl -n "$NS" logs -l app=minitube-nfs-prep --tail=80 || true
  exit 1
fi
hostexec k8s-master minitube-showmount "set -euo pipefail; showmount -e ${NFS_SERVER}"

echo ">> PV/PVC + postgres + config"
kubectl apply -f k8s/minitube/pvc.yaml
kubectl apply -f k8s/minitube/configmap.yaml
kubectl apply -f k8s/minitube/postgres.yaml
kubectl -n "$NS" rollout status statefulset/minitube-pg --timeout=180s

echo ">> dump local compose postgres if it is up"
DUMP=/tmp/minitube-cutover.sql
dumped=0
if docker compose exec -T postgres pg_dump -U minitube --no-owner --no-acl minitube > "$DUMP" 2>/dev/null; then
  dumped=1
fi
if [ "$dumped" = 1 ] && [ -s "$DUMP" ]; then
  echo "   restoring into k8s postgres ($(wc -c < "$DUMP") bytes)"
  kubectl -n "$NS" exec -i minitube-pg-0 -- psql -U minitube -d minitube -v ON_ERROR_STOP=1 < "$DUMP"
else
  echo "   skip dump (local postgres not reachable)"
fi

echo ">> copy media to worker2 hostPath (NFS export already has the same directory)"
kubectl -n "$NS" delete pod minitube-media-copy --ignore-not-found
cat <<'EOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: minitube-media-copy
  namespace: minitube
spec:
  nodeSelector:
    kubernetes.io/hostname: k8s-worker2
  restartPolicy: Never
  containers:
    - name: copy
      image: debian:12-slim
      imagePullPolicy: IfNotPresent
      command: ["sleep", "7200"]
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      hostPath:
        path: /data/minitube
        type: DirectoryOrCreate
EOF
kubectl -n "$NS" wait --for=condition=Ready pod/minitube-media-copy --timeout=120s
SRC_ROOT="$ROOT/api/data"
for dir in uploads srs-hls live-hls; do
  if [ -d "$SRC_ROOT/$dir" ]; then
    echo "   tar $dir"
    tar cf - -C "$SRC_ROOT" "$dir" | kubectl -n "$NS" exec -i minitube-media-copy -- tar xf - -C /data
  fi
done
kubectl -n "$NS" exec minitube-media-copy -- du -sh /data /data/uploads /data/srs-hls /data/live-hls || true
kubectl -n "$NS" delete pod minitube-media-copy --wait=false

echo ">> build and load images"
chmod +x k8s/minitube/build-images.sh
MASTER_IP="$MASTER_IP" k8s/minitube/build-images.sh

echo ">> stop host processes that bind 8080 / 1935"
docker compose stop srs postgres 2>/dev/null || true
for pid in $(ss -lptn 'sport = :8080' 2>/dev/null | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | sort -u); do
  comm=$(ps -o comm= -p "$pid" 2>/dev/null || true)
  case "$comm" in
    api|worker|go) echo "   stopping $comm pid $pid"; kill "$pid" || true ;;
  esac
done
sleep 1
if kubectl -n "$NS" get pod minitube-media-copy >/dev/null 2>&1; then
  echo ">> recopy uploads after stopping writers"
  tar cf - -C "$ROOT/api/data" uploads | kubectl -n "$NS" exec -i minitube-media-copy -- tar xf - -C /data || true
fi

echo ">> API + SRS + GPU workers"
kubectl apply -f k8s/minitube/api.yaml
kubectl apply -f k8s/minitube/srs.yaml
kubectl apply -f k8s/minitube/workers.yaml

kubectl -n "$NS" rollout status deployment/minitube-api --timeout=180s
kubectl -n "$NS" rollout status deployment/minitube-srs --timeout=180s || true
kubectl -n "$NS" rollout status deployment/minitube-worker-nvenc-w2 --timeout=180s || true
kubectl -n "$NS" rollout status deployment/minitube-worker-nvenc-master --timeout=180s || true
kubectl -n "$NS" rollout status deployment/minitube-worker-qsv-w2 --timeout=180s || true

echo ">> healthz"
curl -sf -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/healthz
kubectl -n "$NS" get pods -o wide
echo "cutover done. Cloudflare can keep targeting master:8080. OBS rtmp://$MASTER_IP:1935/live"
echo "worker1 QSV stays Pending until that node is Ready."
