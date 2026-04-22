#!/usr/bin/env bash
# Starts the OpenCloud dev server with the same env as .vscode/launch.json
# ("OpenCloud server" profile). Runs the pre-built binary in
# opencloud/bin/opencloud — rebuild with `go build -o
# opencloud/bin/opencloud ./opencloud/cmd/opencloud` when the tree
# changes. Logs stream to stdout; run under nohup or a terminal
# multiplexer to survive shell exit.
set -euo pipefail

export OC_LOG_LEVEL=debug
export OC_LOG_PRETTY=true
export OC_LOG_COLOR=true
export OC_INSECURE=true
export PROXY_ENABLE_BASIC_AUTH=true
export IDM_CREATE_DEMO_USERS=true

# Advertise the LAN IP so phones / other devices on the same network
# can resolve OIDC endpoints and the Subsonic backend. Fall back to
# localhost if there's no primary LAN interface. Override by exporting
# OC_URL before invoking the script.
if [ -z "${OC_URL:-}" ]; then
  lan_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  if [ -n "${lan_ip}" ]; then
    OC_URL="https://${lan_ip}:9200"
  else
    OC_URL="https://localhost:9200"
  fi
fi
export OC_URL
export OC_ADMIN_USER_ID="some-admin-user-id-0000-000000000000"
export IDM_ADMIN_PASSWORD=admin
export OC_SYSTEM_USER_ID="some-system-user-id-000-000000000000"
export OC_SYSTEM_USER_API_KEY="some-system-user-machine-auth-api-key"
export OC_JWT_SECRET="some-opencloud-jwt-secret"
export OC_MACHINE_AUTH_API_KEY="some-opencloud-machine-auth-api-key"
export OC_TRANSFER_SECRET="some-opencloud-transfer-secret"
export COLLABORATION_WOPI_SECRET="some-wopi-secret"
export IDM_SVC_PASSWORD="some-ldap-idm-password"
export GRAPH_LDAP_BIND_PASSWORD="some-ldap-idm-password"
export IDM_REVASVC_PASSWORD="some-ldap-reva-password"
export GROUPS_LDAP_BIND_PASSWORD="some-ldap-reva-password"
export USERS_LDAP_BIND_PASSWORD="some-ldap-reva-password"
export AUTH_BASIC_LDAP_BIND_PASSWORD="some-ldap-reva-password"
export IDM_IDPSVC_PASSWORD="some-ldap-idp-password"
export IDP_LDAP_BIND_PASSWORD="some-ldap-idp-password"
export GATEWAY_STORAGE_USERS_MOUNT_ID="storage-users-1"
export STORAGE_USERS_MOUNT_ID="storage-users-1"
export GRAPH_APPLICATION_ID="application-1"
export OC_SERVICE_ACCOUNT_ID="service-account-id"
export OC_SERVICE_ACCOUNT_SECRET="service-account-secret"
export SEARCH_EXTRACTOR_TYPE=tika
export SEARCH_EXTRACTOR_TIKA_TIKA_URL="http://host.docker.internal:9998"
export SEARCH_EXTRACTOR_CS3SOURCE_INSECURE=true

exec ./opencloud/bin/opencloud server
