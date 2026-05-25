resource "pangolin_idp_org" "example" {
  idp_id       = pangolin_idp.example.id
  org_id       = "my-org"
  role_mapping = "'Member'"
  org_mapping  = "'my-org'"
}
