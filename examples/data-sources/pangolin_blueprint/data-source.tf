data "pangolin_blueprints" "all" {}

# Pick the most recent blueprint by ID (insertion order is roughly
# monotonic) and pull the full contents for inspection.
locals {
  latest_blueprint_id = max([
    for b in data.pangolin_blueprints.all.blueprints : b.id
  ]...)
}

data "pangolin_blueprint" "latest" {
  id = local.latest_blueprint_id
}

# Audit-friendly outputs.
output "latest_apply_outcome" {
  value = {
    id        = data.pangolin_blueprint.latest.id
    name      = data.pangolin_blueprint.latest.name
    succeeded = data.pangolin_blueprint.latest.succeeded
    message   = data.pangolin_blueprint.latest.message
  }
}

# Dump the raw contents to disk for review.
resource "local_file" "latest_blueprint_dump" {
  filename = "${path.module}/latest-blueprint.yaml"
  content  = data.pangolin_blueprint.latest.contents
}
