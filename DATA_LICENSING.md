# Data licensing — AVISO+ / FES ocean tide model

This service redistributes information derived from the FES ocean tide model,
which is an **AVISO+ Product** and is **not** covered by this repository's MIT
`LICENSE`. That file governs our code; the terms below govern the data.

**Primary source — read this before changing anything here:**
<https://www.aviso.altimetry.fr/fileadmin/documents/data/License_Aviso.pdf>
("License to Use AVISO+ Products", Issue 19 — February 2024)

Everything below is a summary of that document, with clause numbers so each
claim can be checked against the original. AVISO+ may revise the license at any
time (§12), so treat this file as a reading of Issue 19 and re-check the PDF
before relying on it. It is not legal advice.

## The short version

We are clear to run this API commercially, because we use FES **heights**, and
those carry the Standard license, which grants commercial redistribution of
derived products. Two obligations bind us in practice: we must never serve the
FES grids in their original form (§5.1), and every product built on them must
carry the exact credit line **"Generated using AVISO+ Products"** (§5.2).

## Which FES product we use, and why it matters

The license splits FES in two, and the halves have **different terms**:

| AVISO+ auxiliary product | License | Commercial use |
|---|---|---|
| Heights and load effects from ocean tide model (FES) | **Standard** (§3) | **Permitted** |
| Currents from ocean tide model (FES) | **Restricted** (§4) | **Prohibited** without a separate agreement |

We use **heights** only. We do not use, and must not start using, FES
**currents** — §4 grants those "for non-commercial purposes ONLY", and the
licensee "makes a commitment to make no commercial use of restricted AVISO+
products. Commercial use is subject to separate agreement and license as
referred in section 14."

If someone ever wants a current/stream feature, that is not a small addition.
It needs a separate license negotiated with AVISO+ (<aviso@altimetry.fr>).

## What the Standard license grants (§3)

> The Licensee is hereby granted a worldwide, non-exclusive, royalty free
> license […] to:
> 1. make and use such reasonable copies of AVISO+ Products for internal use and
>    back up purposes;
> 2. modify, adapt, develop, create and distribute Value Added Products or
>    Derivative Work from AVISO+ Products **for any purpose (scientific,
>    operational, commercial, etc.).**

Note the asymmetry: clause 1 allows copies of the *product* only for **internal
use and backup**. Distribution rights attach only to **Value Added Products** and
**Derivative Work**, per clause 2.

The definitions in §1:

- **Value Added Product** — "a product which has been created by modifying an
  AVISO+ Product to a higher processing level which retains the original source
  image data and pixel structure".
- **Derivative Work** — "any product and/or information which has been derived,
  developed and irreversibly modified from AVISO+ Product(s) or Value Added
  Product(s) and does not retain the original pixel structure".
- **Commercial Use** — "any development of Value Added Products, Derivative work
  or services based on AVISO+ products which will contribute to a sale, a
  license or any other commercial activity."

Where our outputs land:

- `GET /v1/tides/parameters` returns harmonic constituents **interpolated to a
  single lat/lon**. That is a higher processing level over the source grid — a
  Value Added Product — and §3.2 permits distributing it, commercially included.
- `GET /v1/tides/predictions`, and the water levels the client computes from the
  constituents, are irreversibly derived and keep no grid structure — Derivative
  Work, likewise distributable under §3.2.

## The two obligations that constrain the code

### §5.1 — never serve the model in its original form

> The Licensee makes a commitment to **not distribute any AVISO+ Product in its
> original form via any media.**

The FES NetCDF grids we download under `data/` are the AVISO+ Product in its
original form. They are for computing responses, and they must not leave this
server. Concretely, do **not** add an endpoint that returns a grid, a tile, a
NetCDF file, or a bulk dump of the raw amplitude/phase fields — point
interpolation is what keeps us on the right side of this clause.

### §5.2 — the credit line is prescribed, not paraphrasable

> The Licensees will ensure that original AVISO+ products - or value added
> products or derivative works developed from AVISO+ Products - including
> publications and pictures shall credit AVISO+ by explicitly making mention of
> the originator **in the following manner**: […]
> 2. **"Generated using AVISO+ Products"** for all other AVISO+ products.

(Clause 1 of §5.2 prescribes "Generated using AVISO+/CTOH Products" instead,
for CTOH products, plus a bibliographic reference. FES heights are not a CTOH
product, so clause 2 is the one that applies to us.)

This is a required string, not a description of the kind of credit to give.
Naming the model, or crediting "AVISO+" in prose, does not satisfy it. Anything
we ship on top of FES — this API's responses and documentation, and every client
of it, including the Shiomi app — must carry that sentence verbatim.

## Other clauses worth knowing

- **§2 — the license runs 5 years** from download of the product. Downloading
  again renews it; a long-lived deployment on old data eventually needs a
  refresh.
- **§7.1 — the IP in AVISO+ Products stays with CNES/CTOH**, protected as
  copyright and as a *database right* under French law and EU Directive 96/9.
  A database right can bite even where copyright would not.
- **§7.2 — what we build is ours**: "All new Intellectual Property Rights created
  as a result of modifying or adapting AVISO+ Products will be owned by the
  Licensee." Our engine and our derived predictions are our property; the
  underlying model is not.
- **§5.4 — records**: the licensee must keep records tracing use of AVISO+
  Products, produce them to AVISO+ on reasonable notice, and **propagate this
  requirement into all descending licenses**.
- **§10.2 — breach**: violations "relating to commercial use and
  non-redistribution" get the licensee "excluded from access to AVISO+
  products", on top of any other remedy.
- **§11 — governing law** is French, with ICC arbitration.

## The escape hatch: EOT20

The loader also reads **EOT20**, which is **CC BY 4.0** and needs no AVISO+
registration — see [FES_SETUP.md](FES_SETUP.md#eot20-registration-free-alternative).
Nothing above applies to it: CC BY 4.0 permits commercial use and redistribution
of the data itself, and asks only for attribution. If the AVISO+ terms ever
become inconvenient — the 5-year clock, the §5.4 record-keeping, a feature that
would need currents — switching the model is the cheaper move, because the same
code path already loads it.

## Consequences for App Store submission (Shiomi)

The App Store asks, under App Information → Content Rights, whether the app
contains, shows, or accesses third-party content, and if so requires the
developer to confirm they hold the rights to use it.

The answer is **yes, and we hold the rights**. The tide data originates in
AVISO+ Products whose IP belongs to CNES/CTOH (§7.1), and §3.2 expressly grants
the right to distribute derived products commercially. Both halves of the
declaration are satisfied — provided the app carries the §5.2 credit line.

## Checklist before shipping anything new

- [ ] Does it use FES **currents**? If yes, stop — Restricted license, and
      commercial use needs a separate agreement (§4).
- [ ] Does it expose FES data **in its original form** (grids, tiles, NetCDF,
      bulk fields)? If yes, stop — §5.1.
- [ ] Does the surface carry **"Generated using AVISO+ Products"** verbatim?
      If not, add it — §5.2.
