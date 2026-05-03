#!/bin/sh
set -e

echo "Starting setup script..."
echo "Current Environment: $APP_ENV"

# Example operations
mkdir -p /tmp/honey-demo
cp /tmp/index.html /tmp/honey-demo/
chmod 644 /tmp/honey-demo/index.html

echo "Setup completed successfully."
