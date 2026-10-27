# The selected beta hostname is private Tailnet/split-DNS only. Both public
# Cloudflare entrypoints must reject beta before requiring credentials, looking
# up tools, creating temporary files, or invoking curl.
beta_guard_curl() {
	printf '%s\n' "$*" >> "$BETA_GUARD_CURL_LOG"
	return 99
}
curl() { beta_guard_curl "$@"; }
export -f beta_guard_curl curl
BETA_GUARD_CURL_LOG="$fixture/beta-guard-curl.calls"
: >"$BETA_GUARD_CURL_LOG"
if (
	unset CLOUDFLARE_API_TOKEN
	export HELM_DEPLOY_ENVIRONMENT=beta BETA_GUARD_CURL_LOG
	"$CLOUDFLARE" publish
) >"$fixture/cloudflare-beta-guard.out" 2>&1; then
	fail 'Cloudflare beta public provisioning unexpectedly succeeded'
fi
contains 'private Tailnet/split-DNS hostname' "$fixture/cloudflare-beta-guard.out"
[[ ! -s "$BETA_GUARD_CURL_LOG" ]] || fail 'Cloudflare beta guard invoked curl'
: >"$BETA_GUARD_CURL_LOG"
if (
	unset CLOUDFLARE_API_TOKEN
	export HELM_DEPLOY_ENVIRONMENT=beta BETA_GUARD_CURL_LOG
	"$VALIDATE"
) >"$fixture/validate-beta-guard.out" 2>&1; then
	fail 'live beta public validation unexpectedly succeeded'
fi
contains 'private Tailnet/split-DNS hostname' "$fixture/validate-beta-guard.out"
[[ ! -s "$BETA_GUARD_CURL_LOG" ]] || fail 'live beta guard invoked curl'
unset -f curl beta_guard_curl
unset BETA_GUARD_CURL_LOG

# Exercise Cloudflare's exact origin, ingress, and DNS predicates with mocked
# API responses. The production functions reject alternate loopback, host,
# scheme, route, fallback, and DNS forms rather than normalizing them.
if CLOUDFLARE_API_TOKEN=fixture-token ROADMAP_PUBLIC_ORIGIN=https://evil.example \
	"$CLOUDFLARE" publish >"$fixture/noncanonical-cloudflare-origin.out" 2>&1; then
	fail 'Cloudflare prepare accepted a noncanonical public origin'
fi
if CLOUDFLARE_API_TOKEN=fixture-token ROADMAP_PUBLIC_ORIGIN=https://evil.example \
	"$VALIDATE" >"$fixture/noncanonical-live-origin.out" 2>&1; then
	fail 'live validation accepted a noncanonical public origin'
fi

source <(awk '/^validate_tunnel_config\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^validate_dns_record\(\)/,/^}/' "$CLOUDFLARE")
source <(awk '/^upsert_dns\(\)/,/^}/' "$CLOUDFLARE")
ACCOUNT_ID=fixture-account
ZONE_ID=fixture-zone
PUBLIC_HOST=tc.shanekanterman.dev
cloudflare_fixture_team=team
cloudflare_fixture_ui_aud=ui-audience
cloudflare_fixture_api_aud=api-audience
cloudflare_fixture_tunnel_id=tunnel-fixture
cloudflare_topology_response=
cf_request() { printf '%s' "$cloudflare_topology_response"; }
cloudflare_canonical_tunnel=$(jq -cn \
	--arg host "$PUBLIC_HOST" --arg team "$cloudflare_fixture_team" \
	--arg ui "$cloudflare_fixture_ui_aud" --arg api "$cloudflare_fixture_api_aud" \
	'{result:{config:{ingress:[
		{hostname:$host,service:"http://127.0.0.1:8080",originRequest:{access:{required:true,teamName:$team,audTag:[$ui,$api]}}},
		{service:"http_status:404"}
	]}}}')
cloudflare_topology_response=$cloudflare_canonical_tunnel
validate_tunnel_config "$cloudflare_fixture_team" "$cloudflare_fixture_ui_aud" \
	"$cloudflare_fixture_api_aud" "$cloudflare_fixture_tunnel_id" \
	>"$fixture/cloudflare-canonical-tunnel.out" 2>&1 \
	|| fail 'canonical Cloudflare tunnel ingress was rejected'
for cloudflare_origin in \
	http://localhost:8080 \
	https://127.0.0.1:8080 \
	http://10.0.0.38:8080 \
	http://roadmap:8080 \
	http://127.0.0.1:8080.evil.example; do
	cloudflare_topology_response=$(jq -c --arg origin "$cloudflare_origin" \
		'.result.config.ingress[0].service=$origin' <<<"$cloudflare_canonical_tunnel")
	if validate_tunnel_config "$cloudflare_fixture_team" "$cloudflare_fixture_ui_aud" \
		"$cloudflare_fixture_api_aud" "$cloudflare_fixture_tunnel_id" \
		>"$fixture/cloudflare-origin-${cloudflare_origin//[^A-Za-z0-9]/_}.out" 2>&1; then
		fail "Cloudflare accepted noncanonical tunnel origin: $cloudflare_origin"
	fi
done
for cloudflare_bad_topology in extra-route wrong-fallback extra-audience extra-origin-key; do
	case "$cloudflare_bad_topology" in
		extra-route)
		cloudflare_topology_response=$(jq -c \
			'.result.config.ingress += [{hostname:"unexpected.example",service:"http://127.0.0.1:8080"}]' \
			<<<"$cloudflare_canonical_tunnel") ;;
		wrong-fallback)
		cloudflare_topology_response=$(jq -c \
			'.result.config.ingress[1].service="http://127.0.0.1:8080"' \
			<<<"$cloudflare_canonical_tunnel") ;;
		extra-audience)
		cloudflare_topology_response=$(jq -c \
			'.result.config.ingress[0].originRequest.access.audTag += ["unexpected"]' \
			<<<"$cloudflare_canonical_tunnel") ;;
		extra-origin-key)
		cloudflare_topology_response=$(jq -c \
			'.result.config.ingress[0].originRequest.access.extra=true' \
			<<<"$cloudflare_canonical_tunnel") ;;
	esac
	if validate_tunnel_config "$cloudflare_fixture_team" "$cloudflare_fixture_ui_aud" \
		"$cloudflare_fixture_api_aud" "$cloudflare_fixture_tunnel_id" \
		>"$fixture/cloudflare-topology-$cloudflare_bad_topology.out" 2>&1; then
		fail "Cloudflare accepted malformed tunnel topology: $cloudflare_bad_topology"
	fi
done

cloudflare_canonical_dns=$(jq -cn --arg host "$PUBLIC_HOST" \
	--arg target "$cloudflare_fixture_tunnel_id.cfargotunnel.com" \
	'{result:[{id:"dns-fixture",name:$host,type:"CNAME",content:$target,proxied:true}]}')
cloudflare_topology_response=$cloudflare_canonical_dns
validate_dns_record "$PUBLIC_HOST" "$cloudflare_fixture_tunnel_id" \
	>"$fixture/cloudflare-canonical-dns.out" 2>&1 \
	|| fail 'canonical Cloudflare DNS record was rejected'
for cloudflare_bad_dns in wrong-target unproxied extra-record wrong-type; do
	case "$cloudflare_bad_dns" in
		wrong-target)
			cloudflare_topology_response=$(jq -c \
				'.result[0].content="other.cfargotunnel.com"' <<<"$cloudflare_canonical_dns") ;;
		unproxied)
			cloudflare_topology_response=$(jq -c '.result[0].proxied=false' <<<"$cloudflare_canonical_dns") ;;
		extra-record)
			cloudflare_topology_response=$(jq -c \
				'.result += [{id:"dns-extra",name:"tc.shanekanterman.dev",type:"CNAME",content:"other.cfargotunnel.com",proxied:true}]' \
				<<<"$cloudflare_canonical_dns") ;;
		wrong-type)
			cloudflare_topology_response=$(jq -c '.result[0].type="A"' <<<"$cloudflare_canonical_dns") ;;
	esac
	if validate_dns_record "$PUBLIC_HOST" "$cloudflare_fixture_tunnel_id" \
		>"$fixture/cloudflare-dns-$cloudflare_bad_dns.out" 2>&1; then
		fail "Cloudflare accepted malformed DNS topology: $cloudflare_bad_dns"
	fi
done

# DNS publication must preserve an existing record only when it already points
# at the selected tunnel. A different CNAME is owned by another service and
# must fail closed without issuing a PUT or POST; an absent record remains
# creatable and a correct record remains idempotently reconcilable.
cloudflare_dns_mutation_log="$fixture/cloudflare-dns-upsert.calls"
cloudflare_dns_mode=correct
cloudflare_dns_response=$cloudflare_canonical_dns
cloudflare_dns_request() {
	local method=$1 path=$2 body=${3:-} get_count
	printf '%s %s\n' "$method" "$path" >>"$cloudflare_dns_mutation_log"
	case "$method $path" in
		"GET /zones/$ZONE_ID/dns_records?name=$PUBLIC_HOST")
			get_count=$(grep -Fc -- "GET /zones/$ZONE_ID/dns_records?name=$PUBLIC_HOST" "$cloudflare_dns_mutation_log" || true)
			if [[ "$cloudflare_dns_mode" = absent && "$get_count" = 1 ]]; then
				printf '%s' '{"success":true,"result":[]}'
			else
				printf '%s' "$cloudflare_dns_response"
			fi
			;;
		"PUT /zones/$ZONE_ID/dns_records/dns-fixture"|"POST /zones/$ZONE_ID/dns_records")
			printf '%s' '{"success":true,"result":{}}'
			;;
		*) return 1 ;;
	esac
}
cf_request() { cloudflare_dns_request "$@"; }

: >"$cloudflare_dns_mutation_log"
if ! upsert_dns "$PUBLIC_HOST" "$cloudflare_fixture_tunnel_id" \
	>"$fixture/cloudflare-dns-upsert-correct.out" 2>&1; then
	fail 'correct Cloudflare DNS CNAME was not idempotently reconciled'
fi
[[ "$(grep -Fc -- "PUT /zones/$ZONE_ID/dns_records/dns-fixture" "$cloudflare_dns_mutation_log" || true)" = 1 ]] \
	|| fail 'correct Cloudflare DNS CNAME did not use its existing record'
[[ "$(grep -Fc -- 'POST /zones/fixture-zone/dns_records' "$cloudflare_dns_mutation_log" || true)" = 0 ]] \
	|| fail 'correct Cloudflare DNS CNAME unexpectedly created a duplicate record'

cloudflare_dns_mode=conflict
cloudflare_dns_response=$(jq -c '.result[0].content="other.cfargotunnel.com"' <<<"$cloudflare_canonical_dns")
: >"$cloudflare_dns_mutation_log"
if upsert_dns "$PUBLIC_HOST" "$cloudflare_fixture_tunnel_id" \
	>"$fixture/cloudflare-dns-upsert-conflict.out" 2>&1; then
	fail 'conflicting Cloudflare DNS CNAME was overwritten'
fi
contains 'owned by a different tunnel' "$fixture/cloudflare-dns-upsert-conflict.out"
[[ "$(grep -Ec -- '^(PUT|POST) ' "$cloudflare_dns_mutation_log" || true)" = 0 ]] \
	|| fail 'conflicting Cloudflare DNS CNAME issued a mutation request'

cloudflare_dns_mode=absent
cloudflare_dns_response=$cloudflare_canonical_dns
: >"$cloudflare_dns_mutation_log"
if ! upsert_dns "$PUBLIC_HOST" "$cloudflare_fixture_tunnel_id" \
	>"$fixture/cloudflare-dns-upsert-absent.out" 2>&1; then
	fail 'absent Cloudflare DNS CNAME was not created'
fi
[[ "$(grep -Fc -- 'POST /zones/fixture-zone/dns_records' "$cloudflare_dns_mutation_log" || true)" = 1 ]] \
	|| fail 'absent Cloudflare DNS CNAME did not create exactly one record'
[[ "$(grep -Fc -- 'PUT /zones/fixture-zone/dns_records/dns-fixture' "$cloudflare_dns_mutation_log" || true)" = 0 ]] \
	|| fail 'absent Cloudflare DNS CNAME unexpectedly used PUT'

