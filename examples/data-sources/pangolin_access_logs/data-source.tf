# Query the access audit log for the last 24 hours.
data "pangolin_access_logs" "recent" {
  time_start = timeadd(timestamp(), "-24h")
  limit      = 100
}

output "access_log_total" {
  value = data.pangolin_access_logs.recent.total
}

# Each entry is a JSON string, jsondecode() to access fields.
output "access_log_first_entry" {
  value = length(data.pangolin_access_logs.recent.entries) > 0 ? jsondecode(data.pangolin_access_logs.recent.entries[0]) : null
}

# filter_attributes is a map of dimension -> JSON-encoded array.
output "access_log_actors_seen" {
  value = jsondecode(lookup(data.pangolin_access_logs.recent.filter_attributes, "actors", "[]"))
}
