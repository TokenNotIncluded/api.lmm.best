# Independent RSA-SHA256 fixtures

These synthetic payloads were signed with OpenSSL using RSA PKCS#1 v1.5 /
SHA-256 over the exact bytes `1700000000000.` followed by the JSON file.
Only the disposable public key and signatures are committed. The private key
is not required by tests and is not committed.

Both test.json and prod.json are signed by the **test** fixture key. The latter
must fail when the production verifier key differs, even though its signature
is mathematically valid under the test key. Fixed-time verifier tests cover
Pancake SDK v0.9.0's 45-minute past / 60-second future replay window.
