#!/usr/bin/env python3
"""De-interleave the CRC layer from a Beken BK7252 raw flash dump.

The BK7252's `beken_onchip_crc` FAL device is not a naming quirk: physical flash
stores 32 data bytes followed by a 2-byte CRC, every 34 bytes, aligned to
physical offset 0.  A raw dump taken with `bk7231tools read_flash` therefore has
two junk bytes injected every 32 bytes of real content, which desynchronises any
disassembler and truncates strings.

CRC parameters (brute-forced against this dump, then verified over all 61680
blocks): CRC-16, poly 0x8005, init 0xFFFF, non-reflected in and out, xorout 0,
stored big-endian.

Erased flash carries 0xFFFF as its CRC rather than the computed value.  Note
this is not recoverable from the logical image alone: a *written* block that
happens to be 32 bytes of 0xFF padding stores a real computed CRC, and 7 such
blocks exist in this dump.  So --re-crc always computes the CRC, and the
round-trip check skips all-0xFF blocks (see reinterleave()).

Usage:
    decrc.py DUMP --verify
    decrc.py DUMP -o flash_logical.bin
    decrc.py DUMP -o app.bin --slice 0x10000:0x178000
    decrc.py LOGICAL -o rebuilt.bin --re-crc [--reference DUMP]
"""

import argparse
import sys

BLOCK_DATA = 32
BLOCK_CRC = 2
BLOCK = BLOCK_DATA + BLOCK_CRC
ERASED = b"\xff" * BLOCK_DATA

# Physical offset above which flash is NOT CRC-covered.  Every non-erased block
# below this verifies; the only records above it are raw (an RBL header near
# 0x1ad790 and the factory config sector at 0x1f4000).
CRC_REGION_END = 0x1A0000


def crc16(data):
    """CRC-16/0x8005, init 0xFFFF, non-reflected, xorout 0."""
    crc = 0xFFFF
    for byte in data:
        crc ^= byte << 8
        for _ in range(8):
            crc = ((crc << 1) ^ 0x8005) & 0xFFFF if crc & 0x8000 else (crc << 1) & 0xFFFF
    return crc


def deinterleave(raw):
    """Strip the CRC bytes: raw physical flash -> logical image."""
    out = bytearray()
    for off in range(0, len(raw) - BLOCK + 1, BLOCK):
        out += raw[off:off + BLOCK_DATA]
    return bytes(out)


def reinterleave(logical):
    """Re-insert CRC bytes: logical image -> raw physical flash.

    The CRC is always computed.  Blocks that are still erased on the real
    device store 0xFFFF instead, but nothing in the logical image distinguishes
    those from written 0xFF padding, so they cannot be reproduced faithfully -
    a computed CRC over erased content is the safe choice, since it is what the
    flash controller would write anyway.
    """
    out = bytearray()
    for off in range(0, len(logical), BLOCK_DATA):
        data = logical[off:off + BLOCK_DATA]
        out += data
        if len(data) < BLOCK_DATA:  # trailing partial block carries no CRC
            break
        out += crc16(data).to_bytes(BLOCK_CRC, "big")
    return bytes(out)


def verify(raw):
    """Check every block's CRC. Returns (checked, erased, bad_offsets)."""
    checked = erased = 0
    bad = []
    for off in range(0, len(raw) - BLOCK + 1, BLOCK):
        data = raw[off:off + BLOCK_DATA]
        stored = int.from_bytes(raw[off + BLOCK_DATA:off + BLOCK], "big")
        if data == ERASED and stored == 0xFFFF:
            erased += 1
            continue
        checked += 1
        if crc16(data) != stored:
            bad.append(off)
    return checked, erased, bad


def parse_slice(text):
    lo, _, hi = text.partition(":")
    return int(lo, 0), int(hi, 0)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("infile")
    ap.add_argument("-o", "--output", help="write the converted image here")
    ap.add_argument("--verify", action="store_true",
                    help="report per-block CRC status of a raw dump")
    ap.add_argument("--re-crc", action="store_true",
                    help="reverse: re-insert CRC bytes into a logical image")
    ap.add_argument("--reference", help="with --re-crc, diff the result against this raw dump")
    ap.add_argument("--slice", help="LO:HI offsets (in the output's address space)")
    args = ap.parse_args(argv)

    with open(args.infile, "rb") as fh:
        blob = fh.read()
    print(f"{args.infile}: {len(blob)} bytes (0x{len(blob):x})")

    if args.verify:
        checked, erased, bad = verify(blob)
        print(f"blocks checked: {checked}, erased (CRC 0xFFFF by convention): {erased}")
        print(f"CRC failures:   {len(bad)}")
        outside = [b for b in bad if b >= CRC_REGION_END]
        inside = [b for b in bad if b < CRC_REGION_END]
        if outside:
            print(f"  {len(outside)} above 0x{CRC_REGION_END:x} (expected - raw, "
                  f"non-CRC records): {', '.join(hex(b) for b in outside[:8])}"
                  + (" ..." if len(outside) > 8 else ""))
        if inside:
            print(f"  {len(inside)} INSIDE the CRC region - dump may be corrupt: "
                  f"{', '.join(hex(b) for b in inside[:8])}")
            return 1
        print("OK: every block in the CRC region verifies.")

    if args.re_crc:
        result = reinterleave(blob)
        print(f"re-interleaved -> {len(result)} bytes (0x{len(result):x})")
        if args.reference:
            with open(args.reference, "rb") as fh:
                ref = fh.read()
            limit = min(len(result), len(ref), CRC_REGION_END)
            bad, skipped = [], 0
            for off in range(0, limit - BLOCK + 1, BLOCK):
                if result[off:off + BLOCK_DATA] == ERASED:
                    skipped += 1  # erase state is not recoverable; see reinterleave()
                    continue
                if result[off:off + BLOCK] != ref[off:off + BLOCK]:
                    bad.append(off)
            if bad:
                print(f"MISMATCH vs {args.reference} within the CRC region: "
                      f"{len(bad)} blocks, first at 0x{bad[0]:x}")
                return 1
            print(f"round-trip OK: byte-identical to {args.reference} over "
                  f"0x0-0x{limit:x} ({skipped} all-0xFF blocks skipped)")
    elif args.output:
        result = deinterleave(blob)
        print(f"de-interleaved -> {len(result)} bytes (0x{len(result):x})")
    else:
        result = None

    if args.output and result is not None:
        if args.slice:
            lo, hi = parse_slice(args.slice)
            result = result[lo:hi]
            print(f"sliced 0x{lo:x}:0x{hi:x} -> {len(result)} bytes")
        with open(args.output, "wb") as fh:
            fh.write(result)
        print(f"wrote {args.output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
