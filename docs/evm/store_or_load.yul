object "StoreOrLoad" {
    code {
        datacopy(0, dataoffset("runtime"), datasize("runtime"))
        return(0, datasize("runtime"))
    }

    object "runtime" {
        code {
            switch calldatasize()
            case 0 {
                mstore(0, sload(0))
                return(0, 32)
            }
            default {
                sstore(0, calldataload(0))
                mstore(0, sload(0))
                return(0, 32)
            }
        }
    }
}
