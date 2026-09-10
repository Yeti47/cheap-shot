/* Pre-analysis setup for the BK7252 camera firmware (flash_logical.bin).
 *
 * The image is executed in place: the bootloader's vector table at 0x0 and the
 * app's at 0x10000 both hold handler addresses equal to their own flash offset,
 * so the correct base address is 0x0 and file offset == address throughout.
 *
 * The app is built from mixed ARM and Thumb objects with a hard boundary at
 * 0x80000.  Without forcing TMode over the Thumb range, auto-analysis decodes
 * everything above it as garbage, so this must run BEFORE analysis.
 *
 * @category BK7252
 */
import java.math.BigInteger;

import ghidra.app.script.GhidraScript;
import ghidra.program.model.address.Address;
import ghidra.program.model.lang.Register;
import ghidra.program.model.mem.MemoryBlock;
import ghidra.program.model.symbol.SourceType;

public class SetupBK7252 extends GhidraScript {

    private static final long RAM_BASE = 0x00400000L;
    private static final long RAM_SIZE = 0x00050000L;
    private static final long THUMB_START = 0x00080000L;
    private static final long THUMB_END = 0x0013ffffL;
    private static final long BOOTLOADER_ENTRY = 0x00000000L;
    private static final long APP_ENTRY = 0x00010000L;

    private Address addr(long offset) {
        return currentProgram.getAddressFactory().getDefaultAddressSpace().getAddress(offset);
    }

    @Override
    public void run() throws Exception {
        // SRAM is not part of the flash image, but thread stacks and globals
        // live there; without a block, references to it stay unresolved.
        if (currentProgram.getMemory().getBlock("ram") == null) {
            MemoryBlock ram = currentProgram.getMemory()
                .createUninitializedBlock("ram", addr(RAM_BASE), RAM_SIZE, false);
            ram.setRead(true);
            ram.setWrite(true);
            ram.setExecute(false);
            println("created ram block at 0x" + Long.toHexString(RAM_BASE));
        }

        Register tmode = currentProgram.getProgramContext().getRegister("TMode");
        if (tmode == null) {
            printerr("no TMode register - wrong language? expected ARM:LE:32:v5t");
            return;
        }
        currentProgram.getProgramContext()
            .setValue(tmode, addr(THUMB_START), addr(THUMB_END), BigInteger.ONE);
        println("TMode=1 over 0x" + Long.toHexString(THUMB_START)
                + "-0x" + Long.toHexString(THUMB_END));

        for (long entry : new long[] { BOOTLOADER_ENTRY, APP_ENTRY }) {
            Address a = addr(entry);
            currentProgram.getSymbolTable().addExternalEntryPoint(a);
            disassemble(a);
        }
        createLabel(addr(BOOTLOADER_ENTRY), "bootloader_vectors", true, SourceType.USER_DEFINED);
        createLabel(addr(APP_ENTRY), "app_vectors", true, SourceType.USER_DEFINED);
        println("marked vector tables at 0x0 and 0x10000");
    }
}
