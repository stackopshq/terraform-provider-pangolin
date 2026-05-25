# List the backend targets of an existing HTTP resource, with full
# list-view detail (site labels, health-check, routing).
data "pangolin_resource_targets" "app" {
  resource_id = pangolin_resource.app.id
}

# Targets that currently report as healthy.
output "healthy_targets" {
  value = [
    for t in data.pangolin_resource_targets.app.targets : t
    if t.hc_health == "healthy"
  ]
}

# Distinct sites serving this resource.
output "serving_sites" {
  value = distinct([
    for t in data.pangolin_resource_targets.app.targets : t.site_name
  ])
}
