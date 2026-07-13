# Third Party Notices

This software uses the following open source packages:

1. Gin Web Framework (https://github.com/gin-gonic/gin)
   - MIT License

2. go-netcdf (https://github.com/fhs/go-netcdf)
   - MIT License

## FES2014/2022 tidal model (AVISO+)

Generated using AVISO+ Products.

FES data is an AVISO+ Product, distributed under the "License to Use AVISO+
Products" and not under this repository's MIT license. It requires registration
with AVISO+.

- License (the original, authoritative text):
  https://www.aviso.altimetry.fr/fileadmin/documents/data/License_Aviso.pdf
- AVISO+: https://www.aviso.altimetry.fr/
- **What the license means for this project: [DATA_LICENSING.md](DATA_LICENSING.md)**

Three points bind anything built on it, and DATA_LICENSING.md gives the clauses:

- FES **heights** (what we use) carry the Standard license and may be
  redistributed commercially as derived products. FES **currents** do not — they
  are Restricted, non-commercial only.
- The model must never be redistributed in its original form (the grids
  themselves).
- The credit line "Generated using AVISO+ Products" is prescribed verbatim and
  must appear on every product derived from it, this API and its clients
  included.

## EOT20 tidal model (registration-free alternative)

If using EOT20 tidal model data, the following attribution applies:

EOT20 - A global Empirical Ocean Tide model from multi-mission satellite
altimetry. Hart-Davis, M.G., Piccioni, G., Dettmering, D. et al., DGFI-TUM.
https://doi.org/10.17882/79489
License: CC BY 4.0
