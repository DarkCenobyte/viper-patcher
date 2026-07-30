# Metadata policy

VIPR V4 is a content patch format. It does not serialize ownership, timestamps,
ACLs, extended attributes, Linux capabilities, Windows alternate data streams,
or hard-link topology.

Application follows this explicit policy:

- the installed object must remain a regular file;
- ordinary Unix permission bits are preserved from the installed file;
- setuid, setgid, and sticky bits are rejected before an output is prepared;
- Windows does not interpret or transport Unix permission bits;
- ownership, ACL, xattr, capability, DACL, ADS, timestamp, and hard-link
  semantics are outside the V4 compatibility contract.

Rejecting privilege-bearing mode bits avoids silently producing an executable
with security-sensitive metadata that was not authenticated by the patch.
Future metadata transport requires a separately versioned format feature rather
than an implicit V4 extension.
