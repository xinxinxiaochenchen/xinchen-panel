import type { ReactNode } from "react";

export function AdminSection({
  title,
  description,
  children,
  action,
}: {
  title: string;
  description: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <section className="admin-section">
      <div className="admin-section-head">
        <div>
          <span className="section-overline">ADMIN</span>
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}
