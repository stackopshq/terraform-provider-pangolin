# Pull the DNS records Pangolin expects for a domain so another
# provider (Route 53, Infomaniak DNS, etc.) can publish them.
data "pangolin_domain_dns_records" "main" {
  domain_id = "egcq4bwo41tak9o"
}

# Distinct record types in the set.
output "record_types" {
  value = distinct([
    for r in data.pangolin_domain_dns_records.main.records : r.record_type
  ])
}

# Highlight records that have not yet been verified on the authoritative
# nameserver.
output "unverified_records" {
  value = [
    for r in data.pangolin_domain_dns_records.main.records :
    "${r.record_type} ${r.base_domain} -> ${r.value}"
    if !r.verified
  ]
}
