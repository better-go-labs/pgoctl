# Security Policy

## Supported Versions

pgoctl is in active early development. Security fixes are applied to the current
development line.

| Version | Supported                                    |
| ------- | -------------------------------------------- |
| v0.x    | :white_check_mark: (current development, security fixes applied) |

## Reporting a Vulnerability

**Please do not open a public issue for security vulnerabilities.**

Use GitHub's private vulnerability reporting to disclose a security issue
privately:

- [Report a vulnerability](https://github.com/better-go-labs/pgoctl/security/advisories/new)

This creates a private advisory visible only to you and the maintainers.

## Response SLA

- **Acknowledgement:** within 48 hours of your report.
- **Patch:** within 14 days for critical vulnerabilities.

We will keep you informed of progress throughout the process and coordinate
disclosure timing with you.

## Scope and Severity

pgoctl fetches pprof profiles from production services. Because of this, it
handles data and connections that can be highly sensitive. In particular, we
treat the following as **critical severity**:

- Any vector that could exfiltrate profile data.
- Any vector that could expose credentials (tokens, keys, or connection secrets)
  used to reach production services.

If you find an issue in either category, please report it immediately using the
private reporting link above.
