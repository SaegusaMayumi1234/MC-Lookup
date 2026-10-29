#!/usr/bin/env bash
# Pre-deploy helper: materialize config.yaml from config.example.yaml.

set -euo pipefail

cd "$(dirname "$0")/.."

if [ -f config.yaml ]; then
    echo "config.yaml already exists — leaving it untouched."
    exit 0
fi

if [ ! -f config.example.yaml ]; then
    echo "error: config.example.yaml not found" >&2
    exit 1
fi

cp config.example.yaml config.yaml
echo "Created config.yaml from config.example.yaml"
