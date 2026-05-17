import { Check, Gift } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";
import { plans } from "@/lib/mock";

export default function PlansPage() {
  return (
    <AppShell title="套餐">
      <PageHead
        eyebrow="Plans"
        title="套餐和充值"
        description="查看当前套餐、额度和可用升级项。兑换码用于手动充值或活动额度。"
        action={<button className="btn"><Gift /> 兑换码</button>}
      />
      <div className="grid grid-3">
        {plans.map((plan) => (
          <section className="card card-pad" key={plan.name}>
            <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
              <h2>{plan.name}</h2>
              {plan.current ? <span className="badge green"><Check size={14} /> 当前</span> : null}
            </div>
            <p className="metric-value">{plan.price}</p>
            <p className="muted">{plan.quota}</p>
            <p className="muted">{plan.detail}</p>
            <button className={`btn ${plan.current ? "" : "primary"}`} style={{ width: "100%" }} type="button">
              {plan.current ? "当前套餐" : "选择套餐"}
            </button>
          </section>
        ))}
      </div>
      <section className="card card-pad" style={{ marginTop: 16 }}>
        <h2>额度使用</h2>
        <div className="progress"><span style={{ width: "42%" }} /></div>
        <p className="muted" style={{ margin: "10px 0 0" }}>已使用 8.4M / 20M tokens</p>
      </section>
    </AppShell>
  );
}
