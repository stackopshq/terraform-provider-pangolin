# Pull the analytics rollup for the default 7-day window.
data "pangolin_logs_analytics" "weekly" {}

output "weekly_total" {
  value = data.pangolin_logs_analytics.weekly.total_requests
}

output "weekly_blocked" {
  value = data.pangolin_logs_analytics.weekly.total_blocked
}

# Top 5 countries by request volume.
output "top_countries" {
  value = slice(data.pangolin_logs_analytics.weekly.requests_per_country, 0, 5)
}

# Narrow to a single resource over a custom range.
data "pangolin_logs_analytics" "app_monthly" {
  resource_id = tostring(pangolin_resource.app.id)
  time_start  = timeadd(timestamp(), "-720h")
}

output "app_blocked_share" {
  value = (
    data.pangolin_logs_analytics.app_monthly.total_requests == 0
    ? 0
    : data.pangolin_logs_analytics.app_monthly.total_blocked /
      data.pangolin_logs_analytics.app_monthly.total_requests
  )
}
