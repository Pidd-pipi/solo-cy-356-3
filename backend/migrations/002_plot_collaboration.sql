-- 迁移脚本：地块协作模块（plot_members / plot_invitations）。
-- 与 database/init.sql 保持一致；运行时以 GORM AutoMigrate 为权威 schema。
-- 幂等：已存在表/索引则跳过。

CREATE TABLE IF NOT EXISTS plot_members (
    id BIGSERIAL PRIMARY KEY,
    plot_id BIGINT NOT NULL REFERENCES plots(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    role VARCHAR(32) NOT NULL DEFAULT 'helper',
    invited_by BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uniq_plot_member UNIQUE (plot_id, user_id)
);

CREATE TABLE IF NOT EXISTS plot_invitations (
    id BIGSERIAL PRIMARY KEY,
    plot_id BIGINT NOT NULL REFERENCES plots(id),
    inviter_id BIGINT NOT NULL REFERENCES users(id),
    invitee_id BIGINT NOT NULL REFERENCES users(id),
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    responded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_plot_members_plot ON plot_members(plot_id);
CREATE INDEX IF NOT EXISTS idx_plot_members_user ON plot_members(user_id);
CREATE INDEX IF NOT EXISTS idx_plot_invitations_plot ON plot_invitations(plot_id);
CREATE INDEX IF NOT EXISTS idx_plot_invitations_invitee_status ON plot_invitations(invitee_id, status);
