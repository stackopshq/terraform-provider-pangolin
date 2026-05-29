data "pangolin_blueprints" "all" {}

# Most recent N applies by created_at (epoch seconds).
output "recent_apply_audit" {
  value = slice(
    reverse(sort([
      for b in data.pangolin_blueprints.all.blueprints : b.created_at
    ])),
    0, min(10, length(data.pangolin_blueprints.all.blueprints))
  )
}

# Failed applies — useful for alerting on broken IaC pushes.
output "failed_applies" {
  value = [
    for b in data.pangolin_blueprints.all.blueprints : {
      id   = b.id
      name = b.name
    } if !b.succeeded
  ]
}
