#!/bin/sh
# Pre-deploy helper: materialize config.yaml from config.example.yaml.

set -eu

echo "Working directory before cd: $(pwd)"
echo "Script location: $0"

cd "$(dirname "$0")/.."

echo "Working directory after cd: $(pwd)"
echo "Directory contents:"
ls -la

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
