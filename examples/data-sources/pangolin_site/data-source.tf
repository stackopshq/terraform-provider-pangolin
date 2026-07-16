# Look up a Pangolin site by its human-readable nice ID (the slug shown
# in the Pangolin UI). Returns the full live payload - WireGuard
# tunnel info, traffic counters, status - that the list endpoint
# does not expose.
data "pangolin_site" "main" {
  nice_id = "smart-marbled-salamander"
}

output "is_online" {
  value = data.pangolin_site.main.online
}

output "tunnel_endpoint" {
  value = data.pangolin_site.main.endpoint
}

# Drive an alert when traffic exceeds a threshold.
output "high_traffic" {
  value = data.pangolin_site.main.megabytes_in > 1000
}
