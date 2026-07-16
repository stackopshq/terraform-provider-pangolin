# Query admin/mutation actions performed via the Integration API or web UI.
data "pangolin_action_logs" "last_week" {
  time_start = timeadd(timestamp(), "-168h")
  limit      = 500
}

output "action_log_total" {
  value = data.pangolin_action_logs.last_week.total
}

output "actions_seen" {
  value = jsondecode(lookup(data.pangolin_action_logs.last_week.filter_attributes, "actions", "[]"))
}
