data "pangolin_domains" "all" {}

# Domain-based redirect: everything under old.<domain>/docs is sent to the
# new documentation site with a permanent (308) redirect.
resource "pangolin_redirect" "old_docs" {
  name             = "old-docs"
  domain_id        = data.pangolin_domains.all.domains[0].domain_id
  subdomain        = "old"
  destination_host = "https://docs.example.com"

  path_match_type   = "prefix"
  match_path        = "/docs"
  rewrite_path_type = "stripPrefix"
  permanent         = true
}

# Redirect attached to an existing HTTP resource.
resource "pangolin_resource" "web" {
  name      = "my-app"
  subdomain = "app"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
}

resource "pangolin_redirect" "legacy_path" {
  name             = "legacy-path"
  resource_id      = pangolin_resource.web.id
  destination_host = "https://example.com"

  path_match_type   = "exact"
  match_path        = "/legacy"
  rewrite_path_type = "exact"
  rewrite_path      = "/new"
}
