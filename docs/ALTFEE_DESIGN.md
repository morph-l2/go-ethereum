# AltFee Transaction Type - 架构设计说明

## 概述

**AltFee Transaction (交易类型 0x7F)** 是 Morph 链的扩展交易类型，允许用户使用 **ETH 以外的代币（如稳定币）支付 Gas 费用**。

## 为什么需要 AltFee？

### 传统交易的问题

```
用户场景：
- 用户钱包中有 1000 USDT，但没有 ETH
- 想要转账 USDT，但无法支付 Gas（需要 ETH）
- 必须先买 ETH，才能发送交易

结果：用户体验差，门槛高
```

### AltFee 的解决方案

```
使用 AltFee：
- 用户可以直接用 USDT 支付 Gas
- 不需要持有 ETH
- 系统自动处理代币兑换

结果：降低使用门槛，提升用户体验
```

---

## 核心设计

### 1. 交易结构

```go
type AltFeeTx struct {
    // EIP-1559 标准字段
    ChainID    *big.Int
    Nonce      uint64
    GasTipCap  *big.Int        // 小费上限
    GasFeeCap  *big.Int        // Gas 费用上限
    Gas        uint64          // Gas 限制
    To         *common.Address
    Value      *big.Int
    Data       []byte
    AccessList AccessList
    
    // ⭐ AltFee 新增字段
    FeeTokenID uint16          // 支付 Gas 的代币 ID
    FeeLimit   *big.Int        // 使用该代币支付的最大额度
    
    // 签名
    V, R, S    *big.Int
}
```

### 2. 关键字段说明

| 字段 | 类型 | 说明 | 示例 |
|------|------|------|------|
| `FeeTokenID` | uint16 | 代币 ID，0 = ETH | 1 = USDT, 2 = USDC |
| `FeeLimit` | *big.Int | 用该代币支付的最大费用 | 10 USDT (1e7) |
| `GasFeeCap` | *big.Int | 传统 Gas 价格上限（ETH 单位） | 同 EIP-1559 |
| `Gas` | uint64 | Gas 使用量限制 | 21000 |

### 3. 费用计算逻辑

```
实际扣费 = 根据 FeeTokenID 和市场价格计算

步骤：
1. 计算实际 Gas 消耗: gasUsed = 21000 (举例)
2. 计算 ETH 价格: ethFee = gasUsed * effectiveGasPrice
3. 查询代币价格: tokenPrice = getTokenPrice(FeeTokenID)
4. 转换为代币数量: tokenFee = ethFee * ethPrice / tokenPrice
5. 检查限额: require(tokenFee <= FeeLimit)
6. 从用户账户扣除代币
```

---

## 交易类型编号

```go
// core/types/transaction.go
const (
    LegacyTxType       = 0x00  // Legacy (EIP-155)
    AccessListTxType   = 0x01  // EIP-2930
    DynamicFeeTxType   = 0x02  // EIP-1559
    BlobTxType         = 0x03  // EIP-4844
    L1MessageTxType    = 0x7E  // L1 -> L2 消息
    AltFeeTxType       = 0x7F  // ⭐ AltFee 交易
    SetCodeTxType      = 0x04  // EIP-7702
)
```

**为什么是 0x7F？**
- 0x7F 是预留的类型编号
- 避免与以太坊主网的未来类型冲突
- 与 L1MessageTxType (0x7E) 相邻，便于管理

---

## 交易生命周期

### 1. 创建交易

```javascript
// 使用 ethers.js
const tx = {
    type: 0x7F,              // AltFeeTxType
    to: recipientAddress,
    value: ethers.utils.parseEther("1"),
    gasLimit: 21000,
    maxFeePerGas: ethers.utils.parseUnits("100", "gwei"),
    maxPriorityFeePerGas: ethers.utils.parseUnits("2", "gwei"),
    
    // AltFee 特有字段
    feeTokenID: 1,           // 使用 Token ID = 1 (如 USDT)
    feeLimit: ethers.utils.parseUnits("10", 6)  // 最多花费 10 USDT
};
```

### 2. 签名

```go
// 签名内容包含 AltFee 字段
sigHash = Keccak256(RLP([
    chainID,
    nonce,
    gasTipCap,
    gasFeeCap,
    gas,
    to,
    value,
    data,
    accessList,
    feeTokenID,   // ⭐ 包含在签名中
    feeLimit      // ⭐ 包含在签名中
]))
```

### 3. 交易池验证（重要！）

#### 3.1 基础验证

```go
// core/tx_pool.go
func (pool *TxPool) validateTx(tx *types.Transaction) error {
    if tx.Type() == types.AltFeeTxType {
        // 1. 检查是否启用 AltFee
        if !pool.eip1559 {
            return ErrTxTypeNotSupported
        }
        
        // 2. 检查 FeeTokenID 是否有效
        if tx.FeeTokenID() == 0 {
            return errors.New("invalid fee token ID")
        }
        
        // 3. 检查 FeeLimit 是否足够
        if tx.FeeLimit() == nil || tx.FeeLimit().Sign() <= 0 {
            return errors.New("invalid fee limit")
        }
    }
}
```

#### 3.2 CostLimit 检查（⭐ 关键）

不同代币需要不同的余额检查逻辑：

```go
// 检查用户是否有足够的代币支付 Gas
func (pool *TxPool) validateAltFeeCost(tx *types.Transaction, state *state.StateDB) error {
    from, _ := types.Sender(pool.signer, tx)
    feeTokenID := tx.FeeTokenID()
    
    // 1. 获取代币信息
    tokenInfo, err := fees.GetTokenInfo(state, feeTokenID)
    if err != nil {
        return fmt.Errorf("token %d not found", feeTokenID)
    }
    
    if !tokenInfo.IsActive {
        return fmt.Errorf("token %d is not active", feeTokenID)
    }
    
    // 2. 获取用户的代币余额
    tokenBalance := state.GetBalance(from, tokenInfo.TokenAddress)
    
    // 3. 检查余额是否足够 FeeLimit
    if tokenBalance.Cmp(tx.FeeLimit()) < 0 {
        return fmt.Errorf("insufficient token balance: have %s, need %s (token %d)",
            tokenBalance.String(),
            tx.FeeLimit().String(),
            feeTokenID)
    }
    
    // 4. 同时检查 ETH 余额（用于支付交易的 value）
    if tx.Value().Sign() > 0 {
        ethBalance := state.GetBalance(from)
        if ethBalance.Cmp(tx.Value()) < 0 {
            return ErrInsufficientFundsForTransfer
        }
    }
    
    return nil
}
```

#### 3.3 多代币并发处理

交易池需要跟踪每个账户在不同代币下的待处理交易：

```go
// 交易池内部结构
type TxPool struct {
    // ... 其他字段
    
    // 按账户和代币追踪 pending 费用
    // map[address][tokenID] -> total pending fee
    pendingFees map[common.Address]map[uint16]*big.Int
}

// 添加交易时更新 pending 费用
func (pool *TxPool) addTx(tx *types.Transaction) error {
    from, _ := types.Sender(pool.signer, tx)
    
    if tx.Type() == types.AltFeeTxType {
        tokenID := tx.FeeTokenID()
        
        // 获取该账户在该代币下的待处理费用总和
        if pool.pendingFees[from] == nil {
            pool.pendingFees[from] = make(map[uint16]*big.Int)
        }
        
        currentPending := pool.pendingFees[from][tokenID]
        if currentPending == nil {
            currentPending = big.NewInt(0)
        }
        
        // 累加新交易的 feeLimit
        newPending := new(big.Int).Add(currentPending, tx.FeeLimit())
        
        // 检查总的 pending 费用 + 新交易费用是否超过余额
        tokenBalance := pool.currentState.GetBalance(from, tokenID)
        if tokenBalance.Cmp(newPending) < 0 {
            return fmt.Errorf("insufficient balance for pending + new tx: balance=%s, required=%s (token %d)",
                tokenBalance.String(),
                newPending.String(),
                tokenID)
        }
        
        // 更新 pending 费用
        pool.pendingFees[from][tokenID] = newPending
    }
    
    return nil
}
```

#### 3.4 交易替换（Replace by Fee）

```go
// 替换交易时的检查
func (pool *TxPool) replaceTx(oldTx, newTx *types.Transaction) error {
    // 如果两个交易使用不同的代币
    if oldTx.FeeTokenID() != newTx.FeeTokenID() {
        // 需要分别检查两种代币的余额
        
        // 1. 释放旧代币的 pending
        pool.releasePendingFee(oldTx)
        
        // 2. 检查新代币的余额
        if err := pool.validateAltFeeCost(newTx, pool.currentState); err != nil {
            return err
        }
        
        // 3. 占用新代币的 pending
        pool.addPendingFee(newTx)
    } else {
        // 相同代币，只需检查费用差额
        feeDiff := new(big.Int).Sub(newTx.FeeLimit(), oldTx.FeeLimit())
        if feeDiff.Sign() > 0 {
            // 新交易费用更高，检查是否有足够余额
            tokenBalance := pool.currentState.GetBalance(from, newTx.FeeTokenID())
            currentPending := pool.pendingFees[from][newTx.FeeTokenID()]
            
            requiredBalance := new(big.Int).Add(currentPending, feeDiff)
            if tokenBalance.Cmp(requiredBalance) < 0 {
                return ErrInsufficientFunds
            }
        }
    }
    
    return nil
}

### 4. 执行

```go
// rollup/fees/rollup_fee.go
func ApplyAltFeeTransaction(tx *types.Transaction, statedb *state.StateDB) error {
    // 1. 计算实际 Gas 消耗
    gasUsed := receipt.GasUsed
    effectiveGasPrice := tx.EffectiveGasTipValue(baseFee)
    ethFee := new(big.Int).Mul(new(big.Int).SetUint64(gasUsed), effectiveGasPrice)
    
    // 2. 查询代币价格和精度
    tokenInfo := GetTokenInfo(statedb, tx.FeeTokenID())
    
    // 3. 转换为代币数量
    tokenFee := ConvertETHToToken(ethFee, tokenInfo)
    
    // 4. 检查限额
    if tokenFee.Cmp(tx.FeeLimit()) > 0 {
        return errors.New("fee exceeds limit")
    }
    
    // 5. 扣除代币
    statedb.SubBalance(tokenInfo.TokenAddress, from, tokenFee)
    statedb.AddBalance(feeVault, tokenFee)
    
    return nil
}
```

---

## 代币管理

### Token Registry（代币注册表）

```solidity
// 存储在 StateDB 中的代币信息
struct TokenInfo {
    address tokenAddress;    // ERC20 代币合约地址
    uint256 tokenScale;      // 价格基数（如 1e6 for USDT）
    uint256 feeRate;         // 当前价格（相对于 ETH）
    bool isActive;           // 是否激活
}

// 示例
Token ID 0: ETH (特殊，默认)
Token ID 1: USDT (0x...)
    - tokenScale: 1e6
    - feeRate: 3000 (1 ETH = 3000 USDT)
Token ID 2: USDC (0x...)
Token ID 3: DAI (0x...)
```

### 价格更新

```
价格来源：
1. 链上 Oracle (Chainlink, etc.)
2. DEX 价格（Uniswap TWAP）
3. 管理员手动更新（应急）

更新频率：
- 正常情况：每 10 分钟
- 波动大时：每 1 分钟
```

---

## RPC 接口

### 发送 AltFee 交易

```bash
# eth_sendTransaction
curl -X POST http://localhost:8545 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "eth_sendTransaction",
    "params": [{
        "from": "0x...",
        "to": "0x...",
        "value": "0x0",
        "gas": "0x5208",
        "maxFeePerGas": "0x77359400",
        "maxPriorityFeePerGas": "0x77359400",
        "feeTokenID": "0x1",
        "feeLimit": "0x989680"
    }],
    "id": 1
  }'
```

### 查询交易 Receipt

```json
{
    "type": "0x7f",
    "blockHash": "0x...",
    "transactionHash": "0x...",
    "gasUsed": "0x5208",
    
    // AltFee 特有字段
    "feeTokenID": "0x1",
    "feeRate": "0xbb8",      // 价格比率
    "tokenScale": "0xf4240", // 代币精度
    "feeLimit": "0x989680"   // 费用限额
}
```

---

## 与其他交易类型的区别

| 特性 | Legacy (0x00) | EIP-1559 (0x02) | AltFee (0x7F) |
|------|--------------|----------------|---------------|
| **Gas 价格** | gasPrice | baseFee + tip | 同 EIP-1559 |
| **支付代币** | ETH | ETH | ⭐ 任意代币 |
| **费用上限** | gas * gasPrice | gas * maxFeePerGas | ⭐ feeLimit |
| **AccessList** | ❌ | ✅ | ✅ |
| **签名** | EIP-155 | EIP-2718 | EIP-2718 |

---

## 安全考虑

### 1. 价格操纵防护

```
风险：攻击者操纵代币价格，导致用户支付过高费用

缓解措施：
- 使用 TWAP（时间加权平均价格）
- 设置价格变动上限（如 ±10%）
- 多个价格源交叉验证
```

### 2. 余额检查

```
风险：用户代币余额不足，但交易已执行

缓解措施：
- 交易池验证余额
- 执行前再次检查
- 如果余额不足，回退交易
```

### 3. 费用限额保护

```
风险：价格波动导致实际费用超出预期

缓解措施：
- 用户设置 feeLimit
- 如果 tokenFee > feeLimit，交易失败并回退
- 用户明确控制最大支出
```

### 4. 交易池余额检查（⭐ 防止双花）

```
风险：用户余额不足，但发送多笔交易

场景：
- 用户余额 = 100 USDT
- 同时发送 5 笔交易，每笔 feeLimit = 30 USDT
- 如果不检查，可能都进入交易池（实际余额不够）

缓解措施：
1. 累计检查
   - 交易池追踪每个账户的 pending 费用总和
   - 新交易准入条件: balance >= (pending + newFee)

2. 实时更新
   - 交易执行后立即更新余额
   - 重新验证 pending 交易的余额有效性

3. 交易驱逐
   - 如果账户余额降低（如接收到其他交易的扣费）
   - 自动驱逐部分 pending 交易，确保剩余交易可执行
```

### 5. 多代币并发控制

```
风险：不同代币的交易互相干扰

场景：
- 账户同时使用 USDT 和 USDC 发送交易
- 两种代币的余额需要独立追踪

缓解措施：
- 按代币分别维护 pending 费用
  map[address][tokenID] -> pendingFee
- 不同代币的交易相互独立，不影响准入
```

---

## 测试关注点

### 功能测试

```
1. 正常流程
   - 创建 AltFee 交易
   - 签名和发送
   - 验证代币扣费
   - 检查 Receipt

2. 边界情况
   - FeeTokenID = 0（应该失败或使用 ETH）
   - FeeLimit = 0（应该失败）
   - 余额不足
   - 代币未激活

3. 价格测试
   - 价格上涨时的费用
   - 价格下跌时的费用
   - 超过 feeLimit 时的行为

4. ⭐ CostLimit 检查（重要！）
   
   4.1 单笔交易
   - 余额 = 100 USDT, feeLimit = 50 USDT → ✅ 通过
   - 余额 = 100 USDT, feeLimit = 150 USDT → ❌ 拒绝
   - 余额 = 0, feeLimit = 1 USDT → ❌ 拒绝
   
   4.2 多笔待处理交易（同一代币）
   - 余额 = 100 USDT
   - Tx1: feeLimit = 30 USDT (pending) → ✅
   - Tx2: feeLimit = 50 USDT (pending) → ✅ (总计 80)
   - Tx3: feeLimit = 30 USDT (pending) → ❌ (总计 110 > 100)
   
   4.3 多笔待处理交易（不同代币）
   - 余额: 100 USDT, 50 USDC
   - Tx1: Token=1(USDT), feeLimit=80 → ✅
   - Tx2: Token=2(USDC), feeLimit=40 → ✅
   - Tx3: Token=1(USDT), feeLimit=30 → ❌ (USDT 超限)
   - Tx4: Token=2(USDC), feeLimit=15 → ❌ (USDC 超限)
   
   4.4 交易替换（Replace by Fee）
   - 初始: Tx1(Token=1, fee=10)
   - 替换: Tx2(Token=1, fee=15) → ✅ (相同代币，费用增加)
   - 替换: Tx3(Token=2, fee=12) → 需检查 Token2 余额
   
   4.5 交易执行后余额变化
   - 初始余额: 100 USDT
   - Tx1 执行，实际花费 8 USDT → 余额 = 92 USDT
   - Tx2 pending, feeLimit = 50 USDT
   - Tx3 尝试加入, feeLimit = 50 USDT → ❌ (92 < 100)
   
   4.6 混合场景（ETH value + AltFee）
   - ETH 余额 = 1 ETH
   - USDT 余额 = 100 USDT
   - Tx: value=0.5 ETH, feeToken=USDT, feeLimit=50
   - 检查: ETH >= 0.5 ✅ AND USDT >= 50 ✅
```

### 性能测试

```
1. 交易吞吐量
   - AltFee 交易与普通交易混合
   - 测试 TPS 影响

2. Gas 消耗
   - AltFee 比 EIP-1559 多消耗多少 Gas？
   - 代币转账的额外开销

3. 状态访问
   - 查询代币价格的开销
   - StateDB 读写性能
```

### 兼容性测试

```
1. RPC 接口
   - 各种钱包的兼容性
   - ethers.js / web3.js / viem

2. 区块浏览器
   - 正确显示 AltFee 交易
   - 费用计算准确

3. 历史数据
   - 老区块的查询
   - Receipt 格式兼容性
```

---

## 常见问题

### Q1: 如果代币价格波动很大怎么办？

**A**: 用户设置 `feeLimit`，如果实际费用超过限额，交易会失败并回退。建议用户设置略高于预期的限额（如 +20%）。

### Q2: FeeTokenID = 0 是什么意思？

**A**: 0 代表 ETH，即传统的 Gas 支付方式。如果用户不想使用 AltFee，可以设置为 0 或使用普通的 EIP-1559 交易。

### Q3: 如何确保价格准确？

**A**: 系统使用多个价格源（Oracle + DEX），并使用 TWAP 平滑价格波动。管理员也可以手动更新价格（应急情况）。

### Q4: AltFee 交易的 Gas 消耗更高吗？

**A**: 是的，因为需要额外的代币转账和价格查询。预计比普通交易多消耗约 20-30% 的 Gas。

### Q5: 如果代币合约有问题怎么办？

**A**: 代币注册表中有 `isActive` 标志。如果发现代币有问题，管理员可以立即停用，防止进一步损失。

### Q6: 如何防止用户发送余额不足的交易？

**A**: 交易池在准入阶段会进行多层检查：

1. **单笔检查**: 验证 `tokenBalance >= feeLimit`
2. **累计检查**: 考虑该账户所有 pending 交易的 feeLimit 总和
3. **实时更新**: 交易执行后立即更新余额，影响后续交易的准入

示例：
```
余额: 100 USDT
Pending Tx1: feeLimit = 60 USDT
尝试添加 Tx2: feeLimit = 50 USDT
检查: 60 + 50 = 110 > 100 → 拒绝！
```

### Q7: 同一个账户能同时使用不同代币发送多笔交易吗？

**A**: 可以！交易池会分别追踪每种代币的 pending 费用：

```
账户 0x123:
  - Token 1 (USDT): pending = 50 USDT (余额 100)
  - Token 2 (USDC): pending = 30 USDC (余额 80)
  
新交易使用 Token 1 → 只检查 USDT 余额
新交易使用 Token 2 → 只检查 USDC 余额
```

### Q8: 如果交易最终花费的费用小于 feeLimit 怎么办？

**A**: 这是正常的！`feeLimit` 是**上限**，不是实际花费：

1. **执行前**: 检查 `余额 >= feeLimit`（预留足够资金）
2. **执行时**: 计算实际费用 `actualFee = gasUsed * gasPrice (转换为代币)`
3. **执行后**: 只扣除 `actualFee`，多余的不扣

示例：
```
feeLimit = 10 USDT
实际 gasUsed = 21000, 转换后 = 7 USDT
最终扣费 = 7 USDT (不是 10)
用户节省 = 3 USDT
```

---

## 总结

AltFee 交易类型是 Morph 链的创新功能，主要优势：

✅ **降低门槛**：用户无需持有 ETH 即可使用链  
✅ **灵活支付**：支持多种代币支付 Gas  
✅ **兼容性好**：基于 EIP-1559，钱包易于集成  
✅ **安全可控**：feeLimit 机制保护用户  

测试时重点关注：**费用计算准确性**、**价格更新机制**、**余额检查**、**限额保护**。

