#!/usr/bin/env bash

set -Eeuo pipefail

IMAGE="${1:-go-capi-client:latest}"
SSH_LOGIN="${2:-}"
SSH_OPTS="${SSH_OPTS:--o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new}"

usage() {
  echo "Usage: $0 [image] user@ip" >&2
  echo "Example: $0 go-capi-client:latest ubuntu@ip" >&2
}

if [[ ! "$SSH_LOGIN" =~ ^[^@]+@[^@]+$ ]]; then
  echo "The second argument must use the user@ip format." >&2
  usage
  exit 1
fi

SSH_USER="${SSH_LOGIN%%@*}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    exit 1
  fi
}

require_command docker
require_command kubectl
require_command ssh

mapfile -t NODE_HOSTS < <(
  kubectl get nodes \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.addresses[?(@.type=="InternalIP")]}{.address}{end}{"\n"}{end}'
)

if ((${#NODE_HOSTS[@]} == 0)); then
  echo "No nodes found in the current Kubernetes context." >&2
  exit 1
fi

ARCHIVE="$(mktemp "${TMPDIR:-/tmp}/go-capi-client-image.XXXXXX.tar")"
trap 'rm -f -- "$ARCHIVE"' EXIT

echo "Building $IMAGE..."
docker build --tag "$IMAGE" .

echo "Exporting the image..."
docker image save --output "$ARCHIVE" "$IMAGE"

for node_entry in "${NODE_HOSTS[@]}"; do
  IFS=$'\t' read -r node_name node_ip <<<"$node_entry"
  node_host="${node_ip:-$node_name}"
  ssh_target="${SSH_USER}@${node_host}"

  echo "Loading the image on $node_name ($ssh_target)..."
  # shellcheck disable=SC2086
  ssh $SSH_OPTS "$ssh_target" 'sudo sh -eu -c '\''
    if command -v microk8s >/dev/null 2>&1; then
      microk8s ctr image import -
    elif [ -x /snap/bin/microk8s ]; then
      /snap/bin/microk8s ctr image import -
    elif command -v k3s >/dev/null 2>&1; then
      k3s ctr images import -
    elif command -v ctr >/dev/null 2>&1; then
      ctr --namespace k8s.io images import -
    elif command -v nerdctl >/dev/null 2>&1; then
      nerdctl --namespace k8s.io load
    elif command -v docker >/dev/null 2>&1; then
      docker load
    else
      echo "No supported container runtime found (microk8s, k3s, ctr, nerdctl, or docker)." >&2
      exit 1
    fi
  '\''' <"$ARCHIVE"
done

echo "Image $IMAGE loaded on ${#NODE_HOSTS[@]} node(s)."
echo "Use imagePullPolicy: Never in the Deployment."
