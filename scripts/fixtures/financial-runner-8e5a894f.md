# Immutable recovery test input

`financial-runner-8e5a894f.py.gz` contains the exact original financial runner
from commit `b009976199d4441f4e8b465db2c623e9008ba056`, before deployment-tool separation.
The uncompressed SHA-256 is
`8e5a894f3ca806a1c9f86641e3ac96d2b0de310acc251d5f30a5439b285c611b`.

Only the isolated recovery-adapter test loads this fixture. It does not replace
the production recovery owner's original sealed file, paths or authority.
Do not repin the adapter to the mutable current runner to make a test pass.
