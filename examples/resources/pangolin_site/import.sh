# Sites import by numeric ID. Look it up in the Pangolin web UI URL
# (e.g. /sites/4) or via the pangolin_sites data source.
#
# Note: `newt_secret` cannot be recovered from the Pangolin API after
# creation. After importing, it is set to null in state. Existing
# newt connectors keep working with their original secret; new ones
# bound to the imported site will need to be issued out-of-band.
terraform import pangolin_site.example <site_id>
