# Security policy

## Supported versions

f9s is pre-1.0. Security fixes are made on the latest tagged release and the
default branch. Older 0.x releases may not receive backports.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Use
[GitHub's private vulnerability reporting](https://github.com/imankhadiv/f9s/security/advisories/new)
so the report and any proof of concept stay private until a fix is available.

Include the affected f9s and Flink versions, the deployment mode, the expected
impact, and the smallest reproduction you can safely share. Remove cluster
credentials, bearer tokens, private hostnames, logs containing personal data,
and other production secrets before submitting the report.

The maintainer will acknowledge the report, confirm the next update, and
coordinate disclosure through the private advisory. Please allow time for a
fix before publishing details.

## Scope

Reports about credential disclosure, authorization bypasses, unsafe mutating
actions, command execution, path traversal, and sensitive data sent to an AI
provider are especially valuable. General bugs and feature requests belong in
the public issue tracker.
