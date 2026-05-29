data "pangolin_orgs" "all" {}

# Multi-org admin summary.
output "org_summary" {
  value = [
    for o in data.pangolin_orgs.all.orgs : {
      id     = o.org_id
      name   = o.name
      subnet = o.subnet
    }
  ]
}

# Filter to orgs with audit log retention configured.
output "orgs_with_audit" {
  value = [
    for o in data.pangolin_orgs.all.orgs : o.org_id
    if o.settings_log_retention_days_request > 0
  ]
}
