# All active and pending devices (the upstream default filter).
data "pangolin_user_devices" "all" {}

# Top 10 mobile devices by outbound traffic.
data "pangolin_user_devices" "mobile_top" {
  status  = ["active"]
  agent   = "android" # repeat with "ios" or use a for_each to fan out
  sort_by = "megabytesOut"
  order   = "desc"
}

# Devices currently online for a given user.
data "pangolin_user_devices" "alice_online" {
  query  = "alice"
  online = true
  status = ["active"]
}

output "fleet_summary" {
  value = {
    total_active_pending = data.pangolin_user_devices.all.total
    alice_online_count   = data.pangolin_user_devices.alice_online.total
  }
}
