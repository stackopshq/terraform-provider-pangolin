# List all redirects.
data "pangolin_redirects" "all" {}

# Only the enabled redirects attached to a given domain. domain_id is the
# Pangolin domain id (for example "domain2"), not the domain name.
data "pangolin_domains" "all" {}

data "pangolin_redirects" "by_domain" {
  domain_id = one([for d in data.pangolin_domains.all.domains : d.domain_id if d.base_domain == "example.com"])
  enabled   = true
}

output "redirect_names" {
  value = [for r in data.pangolin_redirects.all.redirects : r.name]
}

output "domain_redirect_destinations" {
  value = {
    for r in data.pangolin_redirects.by_domain.redirects : r.nice_id => r.destination_host
  }
}
