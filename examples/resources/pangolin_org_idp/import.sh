# Import format: "{org_id}/{idp_id}"
#
# Note: `client_secret` cannot be recovered after import — set it
# manually in config to avoid a spurious diff on the next plan.
terraform import pangolin_org_idp.authentik my-org/3
