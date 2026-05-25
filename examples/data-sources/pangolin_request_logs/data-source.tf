# Pull the last 7 days of request audit logs (default time range) for the
# configured organization.
data "pangolin_request_logs" "recent" {}

# Refine with filters: deny decisions on a specific resource over the last 24h.
data "pangolin_request_logs" "denied_yesterday" {
  time_start  = timeadd(timestamp(), "-24h")
  resource_id = pangolin_resource.app.id
  action      = "deny"
  limit       = 500
}

output "denied_count" {
  value = data.pangolin_request_logs.denied_yesterday.total
}

# Decoding extra fields from raw_json (status code, user agent, etc.):
output "denied_status_codes" {
  value = [
    for e in data.pangolin_request_logs.denied_yesterday.entries :
    jsondecode(e.raw_json).statusCode
  ]
}
