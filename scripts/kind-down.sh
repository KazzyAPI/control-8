#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
kind delete cluster --name c8-test --kubeconfig tmp/kubeconfig
