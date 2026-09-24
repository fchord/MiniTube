#!/usr/bin/env bash
# Build MiniTube images and load them into containerd on Ready nodes (no sudo).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
MASTER_IP="${MASTER_IP:-192.168.43.111}"
TAR_DIR=/tmp/minitube-images
mkdir -p dist "$TAR_DIR"

echo ">> go build"
(cd api && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../dist/minitube-api ./cmd/api)
(cd api && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../dist/minitube-worker ./cmd/worker)
python3 k8s/minitube/bundle-ffmpeg.py

echo ">> docker build"
build() {
  docker build \
    --build-arg http_proxy= \
    --build-arg https_proxy= \
    --build-arg HTTP_PROXY= \
    --build-arg HTTPS_PROXY= \
    "$@"
}
build -f k8s/minitube/Dockerfile.api -t minitube/api:local .
build -f k8s/minitube/Dockerfile.worker-nvenc -t minitube/worker-nvenc:local .
build -f k8s/minitube/Dockerfile.worker-qsv -t minitube/worker-qsv:local .

echo ">> docker save $TAR_DIR"
docker save minitube/api:local -o "$TAR_DIR/minitube-api.tar"
docker save minitube/worker-nvenc:local -o "$TAR_DIR/minitube-worker-nvenc.tar"
docker save minitube/worker-qsv:local -o "$TAR_DIR/minitube-worker-qsv.tar"

apply_import_job() {
  local node="$1"
  local body="$2"
  local name="minitube-import-${node}"
  kubectl -n kube-system delete job "$name" --ignore-not-found
  cat >"/tmp/${name}.yaml" <<YML
apiVersion: batch/v1
kind: Job
metadata:
  name: ${name}
  namespace: kube-system
spec:
  ttlSecondsAfterFinished: 300
  backoffLimit: 3
  template:
    spec:
      hostNetwork: true
      hostPID: true
      restartPolicy: Never
      nodeSelector:
        kubernetes.io/hostname: ${node}
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
$(echo "$body" | sed 's/^/              /')
YML
  kubectl apply -f "/tmp/${name}.yaml"
  kubectl wait -n kube-system --for=condition=complete "job/${name}" --timeout=600s
}

echo ">> import on k8s-master from $TAR_DIR"
apply_import_job k8s-master "$(cat <<EOF
set -euo pipefail
for img in minitube-api.tar minitube-worker-nvenc.tar minitube-worker-qsv.tar; do
  echo importing \$img
  ctr -n k8s.io images import ${TAR_DIR}/\$img
done
ctr -n k8s.io images ls | grep minitube || true
EOF
)"

PORT=18765
python3 -m http.server "$PORT" --directory "$TAR_DIR" >/tmp/minitube-image-http.log 2>&1 &
HTTP_PID=$!
trap 'kill $HTTP_PID 2>/dev/null || true' EXIT
sleep 0.4

nodes=$(kubectl get nodes --no-headers | awk '$2=="Ready"{print $1}')
for node in $nodes; do
  if [ "$node" = "k8s-master" ]; then
    continue
  fi
  echo ">> import on $node via http://$MASTER_IP:$PORT"
  apply_import_job "$node" "$(cat <<EOF
set -euo pipefail
for img in minitube-api.tar minitube-worker-nvenc.tar minitube-worker-qsv.tar; do
  echo importing \$img
  curl -fsSL http://${MASTER_IP}:${PORT}/\$img | ctr -n k8s.io images import -
done
ctr -n k8s.io images ls | grep minitube || true
EOF
)"
done

echo ">> images ready"
