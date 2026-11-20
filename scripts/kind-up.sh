#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p tmp
kind create cluster --name c8-test --kubeconfig tmp/kubeconfig --wait 120s
