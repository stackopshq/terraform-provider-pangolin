data "pangolin_idps" "all" {}

output "idp_names" {
  value = [for idp in data.pangolin_idps.all.idps : idp.name]
}
