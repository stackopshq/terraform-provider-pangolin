# Organizations import by their string ID (the slug shown in the
# Pangolin web UI URL).
#
# Note: `ssh_ca_private_key` is returned by the API on import and
# lands in state. Treat the Terraform state as a secret accordingly.
terraform import pangolin_org.example <org_id>
