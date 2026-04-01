data "pangolin_site_resources" "all" {}

output "site_resource_names" {
  value = [for sr in data.pangolin_site_resources.all.site_resources : sr.name]
}
