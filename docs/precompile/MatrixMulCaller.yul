object "MatrixMulCaller" {
    code {
        datacopy(0, dataoffset("runtime"), datasize("runtime"))
        return(0, datasize("runtime"))
    }

    object "runtime" {
        code {
            let size := calldatasize()
            let ptr := mload(0x40)
            calldatacopy(ptr, 0, size)

            // staticcall(gas, to, inOffset, inSize, outOffset, outSize)
            if iszero(staticcall(gas(), 0x0123, ptr, size, ptr, 0x80)) {
                revert(0, 0)
            }
            return(ptr, 0x80)
        }
    }
}
