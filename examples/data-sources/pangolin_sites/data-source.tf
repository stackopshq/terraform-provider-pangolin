data "pangolin_sites" "all" {}

output "online_sites" {
  value = [for s in data.pangolin_sites.all.sites : s.name if s.online]
}
