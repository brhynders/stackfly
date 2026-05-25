#!/bin/bash
set -e

STACKFLY_URL="http://localhost:3000"
APP_NAME="hello-world"
REPO_PATH="/tmp/stackfly-dev/repos/${APP_NAME}.git"

echo "==> Deleting old app if it exists..."
curl -sf -X DELETE "${STACKFLY_URL}/apps/${APP_NAME}" -o /dev/null 2>/dev/null || true

echo "==> Creating app '${APP_NAME}'..."
curl -sf -d "name=${APP_NAME}" -L "${STACKFLY_URL}/apps" -o /dev/null
echo "    OK"

echo "==> Initializing test repo from examples/hello-world..."
WORK=$(mktemp -d)
cp -r "$(dirname "$0")/../examples/hello-world/." "$WORK/"
cd "$WORK"
git init -b main
git add -A
git commit -m "initial commit"

echo "==> Pushing to StackFly bare repo at ${REPO_PATH}..."
# Dev container creates repos as root; allow host user to push
git config --global --add safe.directory "$REPO_PATH" 2>/dev/null || true
git remote add stackfly "$REPO_PATH"
git push stackfly main

echo ""
echo "==> Push complete. The post-receive hook should trigger a build."
echo "    Open ${STACKFLY_URL}/apps/${APP_NAME} to watch the build log."
echo ""
echo "==> Waiting 5s then checking deploy status..."
sleep 5

DEPLOY_HTML=$(curl -sf "${STACKFLY_URL}/apps/${APP_NAME}/deployments")

if echo "$DEPLOY_HTML" | grep -q "building"; then
    echo "    Build still in progress. Waiting 30 more seconds..."
    sleep 30
    DEPLOY_HTML=$(curl -sf "${STACKFLY_URL}/apps/${APP_NAME}/deployments")
fi

if echo "$DEPLOY_HTML" | grep -q "deployed"; then
    echo "==> Deploy succeeded!"
    echo ""
    echo "==> Testing the running app..."
    docker exec stackfly-caddy sh -c "apk add -q curl 2>/dev/null; curl -sf http://hello-world-web:5000" 2>/dev/null && echo "" || echo "    (app container may not be reachable yet)"
elif echo "$DEPLOY_HTML" | grep -q "failed"; then
    echo "==> Deploy FAILED. Check the build log at ${STACKFLY_URL}/apps/${APP_NAME}"
else
    echo "==> Unknown status. Check ${STACKFLY_URL}/apps/${APP_NAME}"
fi

# Cleanup
rm -rf "$WORK"
echo ""
echo "==> Done. Visit ${STACKFLY_URL}/apps/${APP_NAME} to manage the app."
