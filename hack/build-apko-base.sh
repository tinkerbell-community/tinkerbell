#!/bin/bash
set -euo pipefail

# Build the apko base image for both architectures
echo "Building apko base image..."
apko build apko.yaml "${BASE_IMAGE_TAG:-tinkerbell-base:latest}" tinkerbell-base.tar --arch amd64,arm64

# Load the image into Docker daemon so ko can use it
echo "Loading apko image into Docker..."
docker load < tinkerbell-base.tar

echo "Apko base image built and loaded: ${BASE_IMAGE_TAG:-tinkerbell-base:latest}"
