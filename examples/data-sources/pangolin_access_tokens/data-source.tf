data "pangolin_access_tokens" "all" {}

# All tokens bound to a single resource.
output "ci_bot_tokens" {
  value = [
    for t in data.pangolin_access_tokens.all.access_tokens : t
    if t.resource_id == pangolin_resource.filebrowser.id
  ]
}

# Tokens about to expire (within the next 24h, epoch milliseconds).
output "expiring_soon" {
  value = [
    for t in data.pangolin_access_tokens.all.access_tokens : t
    if t.expires_at != null && t.expires_at < timestamp() + 86400000
  ]
}
