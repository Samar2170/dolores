#### Architecture
Get AV DAta -> Add Tickertape data -> Extract -> Calculate key metrics -> (Product, Revenue segmentation, Market share, Input risk research) -> Other research -> Audit previous step -> Create Pointers -> Create Report



Volume vs price-led growth
Guidance vs actual growth
Market size / TAM
Market share + trajectory
Competitive landscape
Pricing power
Customer concentration
Geographic concentration
Moat / competitive advantages

Supplier/customer bargaining power
Regulatory risks
Disruption/substitution risk
Management & Governance
Promoter/KMP history
Promoter holding + changes
Related-party transactions
Auditor history
Governance red flags
Capital allocation track record
Acquisitions / buybacks / dividends
Management guidance vs actual delivery
Compensation vs company performance
Pledging / dilution

Cash Flow & Capital Allocation ← I'd make this its own category
CFO
Capex
FCF
FCF conversion
ROIC on incremental capital
Where management is deploying cash
Debt repayment vs acquisitions vs dividends
Valuation ← big one missing from your list


FCF yield
Historical valuation bands
Peer valuation
DCF / intrinsic value
Growth assumptions required to justify current price

Risk Analysis
Business risks
Financial risks
Regulatory risks
Commodity/input risks
FX risk
Customer concentration
Governance risks

Bull/base/bear scenarios
Market / Price Behaviour
Relative performance vs index
Volatility
Drawdowns
Institutional/promoter buying/selling


### Hard data analysis
| Data point     |  Source    | Analysis  | 
| moving averages| indiasm_stock | time to buy, trend |
| mgmtEffetiveness | indiasm_stock | get an idea of efficiency, how business is being done, any red flags? |
| margins       | indiasm_stock    | efficiency, good or no? |
| financial strength | indiasm_stock | financial position |
| valuation | indiasm_stock | decent/ over/under  |
| growth | indiasm_stock | growth, trend, volatility, drawdowns, etc (degree of operating leverage) |
| analyst view | indiasm_stock | sentiment |
| shareholding pattern | indiasm_stock | shareholding pattern |

| marings, returns over years | key_metrics | marign stability etc |

| investor presentation | investor_presentation file in archivus | business summary, products etc., Revenue/Profit/Asset segmentation, customer profile, geographic profile, competitive landscape. |

### New architecture

```mermaid

    A[Company Symbol + Exchange] --> B[Fetch API Data]

    B --> C[Alpha Vantage]
    B --> D[IndiaSM]

    C --> E[Save Raw API Data]
    D --> E

    E --> F[Calculate Margins]

    F --> G[Save Calculated Metrics]

    G --> H[Current Research]

    H --> H1[Products]
    H --> H2[Input / Raw Material Risks]
    H --> H3[Market Share]
    H --> H4[Revenue Segmentation]

    H1 --> I[Save Research]
    H2 --> I
    H3 --> I
    H4 --> I
```