#!/usr/bin/env python3
"""Minimal UART capture/command helper for the camera's msh console.

Uses the system python3 (which has pyserial). Two modes:
  uartcap.py log  <outfile> [seconds]        # passive capture (boot log)
  uartcap.py cmd  <outfile> <seconds> "cmd"  # send one command, capture reply
"""
import sys, time, serial

PORT, BAUD = "/dev/ttyACM0", 115200

def main():
    mode = sys.argv[1]
    out = sys.argv[2]
    secs = float(sys.argv[3]) if len(sys.argv) > 3 else 90.0
    ser = serial.Serial(PORT, BAUD, timeout=1)
    ser.reset_input_buffer()
    if mode == "cmd":
        cmd = sys.argv[4]
        ser.write((cmd + "\r\n").encode())
        ser.flush()
    end = time.time() + secs
    total = 0
    with open(out, "wb") as f:
        while time.time() < end:
            n = ser.in_waiting
            data = ser.read(n if n else 1)
            if data:
                f.write(data); f.flush(); total += len(data)
    ser.close()
    print(f"captured {total} bytes -> {out}")

if __name__ == "__main__":
    main()
