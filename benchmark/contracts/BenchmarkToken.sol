// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title BenchmarkToken - Minimal ERC20 for performance benchmarking
/// @notice Storage layout:
///   slot 0: mapping(address => uint256) balanceOf
///   slot 1: mapping(address => mapping(address => uint256)) allowance
///   slot 2: uint256 totalSupply
///   This layout is important for genesis pre-deployment (storage slot computation).
contract BenchmarkToken {
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    uint256 public totalSupply;

    string public constant name = "Benchmark Token";
    string public constant symbol = "BENCH";
    uint8 public constant decimals = 18;

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);

    function transfer(address to, uint256 value) public returns (bool) {
        require(balanceOf[msg.sender] >= value, "insufficient balance");
        balanceOf[msg.sender] -= value;
        balanceOf[to] += value;
        emit Transfer(msg.sender, to, value);
        return true;
    }

    function approve(address spender, uint256 value) public returns (bool) {
        allowance[msg.sender][spender] = value;
        emit Approval(msg.sender, spender, value);
        return true;
    }

    function transferFrom(address from, address to, uint256 value) public returns (bool) {
        require(balanceOf[from] >= value, "insufficient balance");
        require(allowance[from][msg.sender] >= value, "insufficient allowance");
        balanceOf[from] -= value;
        allowance[from][msg.sender] -= value;
        balanceOf[to] += value;
        emit Transfer(from, to, value);
        return true;
    }
}

