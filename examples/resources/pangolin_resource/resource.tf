data "pangolin_domains" "all" {}

resource "pangolin_resource" "example" {
  name      = "my-app"
  subdomain = "app"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
  protocol  = "tcp"
}
