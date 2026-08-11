#!/usr/bin/env bash
set -Eeuo pipefail

bootstrap=
root=/
while (($#)); do
  case "$1" in
    --bootstrap) bootstrap=${2-}; shift 2 ;;
    --root)
      [[ ${NEPROTO_TEST_MODE:-} == 1 ]] || { printf 'ERROR: --root is test-only\n' >&2; exit 2; }
      root=${2-}; shift 2
      ;;
    *) printf 'usage: cluster-node-install.sh --bootstrap FILE\n' >&2; exit 2 ;;
  esac
done
[[ $EUID -eq 0 || ${NEPROTO_TEST_MODE:-} == 1 ]] || { printf 'ERROR: run as root\n' >&2; exit 1; }
[[ -f $bootstrap && ! -L $bootstrap ]] || { printf 'ERROR: bootstrap file is missing\n' >&2; exit 1; }
command -v jq >/dev/null 2>&1 || {
  [[ ${NEPROTO_TEST_MODE:-} == 1 ]] && { printf 'ERROR: jq is required\n' >&2; exit 1; }
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends jq
}

expected='["acme_email","addresses","cluster_id","domain","http3_path","https_path","master_addresses","master_domain","master_http3_path","master_https_path","master_node_id","master_webrtc_path","mode","name","node_id","peer_credential_id","peer_secret","region","roles","version","webrtc_path"]'
jq -e --argjson expected "$expected" '(. | keys | sort) == $expected and .version == 1' "$bootstrap" >/dev/null || {
  printf 'ERROR: invalid bootstrap schema\n' >&2; exit 1;
}
read_value() { jq -er "$1" "$bootstrap"; }
mode=$(read_value '.mode')
domain=$(read_value '.domain')
addresses=$(jq -er '.addresses | join(",")' "$bootstrap")
email=$(read_value '.acme_email')
cluster_id=$(read_value '.cluster_id')
node_id=$(read_value '.node_id')
name=$(read_value '.name')
region=$(read_value '.region')
master_node_id=$(read_value '.master_node_id')
credential_id=$(read_value '.peer_credential_id')
peer_secret=$(read_value '.peer_secret')
https_path=$(read_value '.https_path')
webrtc_path=$(read_value '.webrtc_path')
http3_path=$(read_value '.http3_path')
master_domain=$(read_value '.master_domain')
master_addresses=$(jq -cer '.master_addresses' "$bootstrap")
master_https_path=$(read_value '.master_https_path')
master_webrtc_path=$(read_value '.master_webrtc_path')
master_http3_path=$(read_value '.master_http3_path')

[[ $mode == bare-metal || $mode == docker ]] || { printf 'ERROR: invalid mode\n' >&2; exit 1; }
[[ $cluster_id =~ ^[a-z0-9][a-z0-9_-]{0,63}$ && $node_id =~ ^[a-z0-9][a-z0-9_-]{0,63}$ && $master_node_id =~ ^[a-z0-9][a-z0-9_-]{0,63}$ ]] || {
  printf 'ERROR: invalid cluster identity\n' >&2; exit 1;
}
[[ $credential_id =~ ^[A-Za-z0-9_-]{22}$ && $peer_secret =~ ^[A-Za-z0-9_-]{43}$ ]] || {
  printf 'ERROR: invalid peer credential\n' >&2; exit 1;
}
jq -e '
  (.addresses | type == "array" and length >= 1 and length <= 8 and all(.[]; type == "string")) and
  (.master_addresses | type == "array" and length >= 1 and length <= 8 and all(.[]; type == "string"))
' "$bootstrap" >/dev/null || { printf 'ERROR: invalid node address list\n' >&2; exit 1; }
for identity in "$domain" "$master_domain"; do
  [[ $identity =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])$ && $identity == *.* && $identity != *..* ]] || {
    printf 'ERROR: invalid cluster server identity\n' >&2; exit 1;
  }
done
for path in "$https_path" "$webrtc_path" "$http3_path" "$master_https_path" "$master_webrtc_path" "$master_http3_path"; do
  [[ $path =~ ^/[a-f0-9]{48}$ ]] || { printf 'ERROR: invalid private transport path\n' >&2; exit 1; }
done
[[ $https_path != "$webrtc_path" && $https_path != "$http3_path" && $webrtc_path != "$http3_path" ]] || {
  printf 'ERROR: duplicate node transport paths\n' >&2; exit 1;
}
[[ $master_https_path != "$master_webrtc_path" && $master_https_path != "$master_http3_path" && $master_webrtc_path != "$master_http3_path" ]] || {
  printf 'ERROR: duplicate master transport paths\n' >&2; exit 1;
}
jq -e '(.roles | type == "array") and (.roles | length >= 1 and length <= 3) and all(.roles[]; . == "ingress" or . == "relay" or . == "egress") and ((.roles | unique | length) == (.roles | length))' "$bootstrap" >/dev/null || {
  printf 'ERROR: invalid node roles\n' >&2; exit 1;
}

path_in_root() { if [[ $root == / ]]; then printf '/%s' "${1#/}"; else printf '%s/%s' "${root%/}" "${1#/}"; fi; }
etc_neproto=$(path_in_root /etc/neproto)
cluster_dir=$etc_neproto/cluster
node_state=$cluster_dir/node.json
peer_dir=$cluster_dir/peers/$master_node_id
peer_map=$cluster_dir/accepted-peers.json
active_directory=$etc_neproto/users/active
peer_secret_file=$peer_dir/secret
old_credential_id=
retry_existing_node=0

if [[ -e $node_state || -L $node_state ]]; then
  [[ -f $node_state && ! -L $node_state ]] || { printf 'ERROR: unsafe existing cluster node state\n' >&2; exit 1; }
  jq -e \
    --arg cluster_id "$cluster_id" --arg node_id "$node_id" --arg master_node_id "$master_node_id" \
    '.cluster_id == $cluster_id and .node_id == $node_id and .master_node_id == $master_node_id' \
    "$node_state" >/dev/null || {
      printf 'ERROR: this host already belongs to a different cluster node\n' >&2; exit 1;
    }
  [[ -f $peer_map && ! -L $peer_map ]] || { printf 'ERROR: existing cluster peer map is unavailable\n' >&2; exit 1; }
  old_credential_id=$(jq -er --arg node_id "$master_node_id" \
    '[.peers[] | select(.node_id == $node_id)] | if length == 1 then .[0].credential_id else empty end' \
    "$peer_map") || { printf 'ERROR: existing master peer identity is invalid\n' >&2; exit 1; }
  [[ $old_credential_id =~ ^[A-Za-z0-9_-]{22}$ ]] || { printf 'ERROR: existing master peer credential is invalid\n' >&2; exit 1; }
  old_active_secret=$active_directory/$old_credential_id.secret
  for existing_secret in "$old_active_secret" "$peer_secret_file"; do
    [[ ! -L $existing_secret ]] || { printf 'ERROR: unsafe existing cluster credential\n' >&2; exit 1; }
    [[ ! -e $existing_secret || -f $existing_secret ]] || { printf 'ERROR: invalid existing cluster credential\n' >&2; exit 1; }
  done
  retry_existing_node=1
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
install_args=(--mode "$mode" --domain "$domain" --addresses "$addresses" --non-interactive \
  --https-path "$https_path" --webrtc-path "$webrtc_path" --http3-path "$http3_path")
[[ -z $email ]] || install_args+=(--email "$email")
if [[ ${NEPROTO_TEST_MODE:-} == 1 ]]; then
  install_args+=(--root "$root" --skip-start)
fi
if ((retry_existing_node == 1)); then
  install_args+=(--cluster-recovery)
fi
"$script_dir/install.sh" "${install_args[@]}"

runtime_cluster_dir=/etc/neproto/cluster
runtime_peer_dir=$runtime_cluster_dir/peers/$master_node_id
runtime_peer_map=$runtime_cluster_dir/accepted-peers.json
install -d -m 0750 "$cluster_dir" "$cluster_dir/peers" "$peer_dir"
active_secret=$etc_neproto/users/active/$credential_id.secret
if ((retry_existing_node == 0)); then
  [[ ! -e $active_secret && ! -L $active_secret && ! -e $peer_secret_file && ! -L $peer_secret_file ]] || {
    printf 'ERROR: cluster peer credential already exists\n' >&2; exit 1;
  }
else
  [[ ! -L $active_secret ]] || { printf 'ERROR: unsafe replacement cluster credential\n' >&2; exit 1; }
  [[ ! -e $active_secret || -f $active_secret ]] || { printf 'ERROR: invalid replacement cluster credential\n' >&2; exit 1; }
fi

write_secret() {
  local target=$1 value=$2 temporary
  temporary=$(mktemp "$(dirname -- "$target")/.credential.XXXXXX")
  printf '%s\n' "$value" >"$temporary"
  chmod 0600 "$temporary"
  mv -f "$temporary" "$target"
}
write_secret "$active_secret" "$peer_secret"
write_secret "$peer_secret_file" "$peer_secret"

client_temporary=$(mktemp "$peer_dir/.client.XXXXXX")
jq -n \
  --arg identity "$master_domain" \
  --argjson addresses "$master_addresses" \
  --arg secret_file "$runtime_peer_dir/secret" \
  --arg https_url "wss://$master_domain$master_https_path" \
  --arg webrtc_url "https://$master_domain$master_webrtc_path" \
  --arg http3_url "https://$master_domain$master_http3_path" \
  '{
    server_identity:$identity,server_addresses:$addresses,secret_file:$secret_file,
    socks_listen:"127.0.0.1:0",https_url:$https_url,webrtc_signaling_url:$webrtc_url,http3_url:$http3_url,
    profile:"web",carrier_policy:"performance",max_cover_overhead_percent:20,
    initial_window_bytes:1048576,max_streams:256,max_parallel_carriers:3,max_socks_connections:256,
    webrtc_timeout:"5s",https_timeout:"10s",http3_timeout:"5s",carrier_cache_ttl:"10m",
    enable_constellation:false,enable_forward_secrecy:true
  }' >"$client_temporary"
chmod 0600 "$client_temporary"
mv -f "$client_temporary" "$peer_dir/client.json"
peer_map_temporary=$(mktemp "$cluster_dir/.accepted-peers.XXXXXX")
jq -n --arg credential_id "$credential_id" --arg node_id "$master_node_id" \
  '{version:1,peers:[{credential_id:$credential_id,node_id:$node_id}]}' >"$peer_map_temporary"
chmod 0600 "$peer_map_temporary"
mv -f "$peer_map_temporary" "$peer_map"

temporary=$(mktemp "$cluster_dir/.node.XXXXXX")
jq '{version,cluster_id,node_id,name,region,roles,master_node_id,installed_at:(now|todateiso8601)}' "$bootstrap" >"$temporary"
chmod 0600 "$temporary"
mv -f "$temporary" "$node_state"

if ((retry_existing_node == 1)) && [[ $old_credential_id != "$credential_id" ]]; then
  rm -f -- "$old_active_secret"
fi

server_config=$etc_neproto/server.json
server_temporary=$(mktemp "$etc_neproto/.server-cluster.XXXXXX")
jq \
  --arg node_id "$node_id" --arg master_node_id "$master_node_id" \
  --arg peer_directory "$runtime_cluster_dir/peers" --arg peer_map "$runtime_peer_map" \
  '. + {
    cluster_node_id:$node_id,cluster_master_node_id:$master_node_id,
    cluster_peer_directory:$peer_directory,cluster_peer_map_file:$peer_map
  }' "$server_config" >"$server_temporary"
chmod 0640 "$server_temporary"
mv -f "$server_temporary" "$server_config"
unset peer_secret

installation_uid=$(jq -er '.service_uid' "$etc_neproto/installation.json")
installation_gid=$(jq -er '.service_gid' "$etc_neproto/installation.json")
chown root:"$installation_gid" "$cluster_dir" "$cluster_dir/peers" "$peer_dir"
chmod 0750 "$cluster_dir" "$cluster_dir/peers" "$peer_dir"
chown "$installation_uid":"$installation_gid" "$active_secret" "$peer_secret_file" "$peer_dir/client.json" "$peer_map"
chown root:"$installation_gid" "$server_config"
chmod 0640 "$server_config"

if [[ ${NEPROTO_TEST_MODE:-} != 1 ]]; then
  if [[ $mode == bare-metal ]]; then
    neproto-server check --config /etc/neproto/server.json
    systemctl restart neproto-server.service caddy.service
  else
    (cd /opt/neproto && docker compose up -d --build)
  fi
  neprotoctl doctor
fi
printf 'NP2 cluster node %s installed\n' "$node_id"
