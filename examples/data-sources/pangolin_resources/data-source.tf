data "pangolin_resources" "all" {}

output "resource_domains" {
  value = [for r in data.pangolin_resources.all.resources : r.full_domain]
}
