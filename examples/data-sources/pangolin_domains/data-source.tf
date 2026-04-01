data "pangolin_domains" "all" {}

output "domains" {
  value = data.pangolin_domains.all.domains
}
