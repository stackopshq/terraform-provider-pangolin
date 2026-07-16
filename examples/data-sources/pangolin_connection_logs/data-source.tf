# Query VPN and tunnel connection lifecycle events.
data "pangolin_connection_logs" "recent" {
  time_start = timeadd(timestamp(), "-24h")
  limit      = 200
}

output "connection_log_total" {
  value = data.pangolin_connection_logs.recent.total
}

output "protocols_seen" {
  value = jsondecode(lookup(data.pangolin_connection_logs.recent.filter_attributes, "protocols", "[]"))
}
