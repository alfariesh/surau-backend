CREATE TABLE IF NOT EXISTS email_templates (
    id UUID PRIMARY KEY,
    key VARCHAR(128) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    category VARCHAR(32) NOT NULL,
    critical BOOLEAN NOT NULL DEFAULT false,
    enabled BOOLEAN NOT NULL DEFAULT true,
    archived_at TIMESTAMP NULL,
    deleted_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_templates_category_check CHECK (category IN ('transactional', 'marketing'))
);

CREATE INDEX IF NOT EXISTS idx_email_templates_category
    ON email_templates(category)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS email_template_versions (
    id UUID PRIMARY KEY,
    template_id UUID NOT NULL REFERENCES email_templates(id) ON DELETE CASCADE,
    lang VARCHAR(8) NOT NULL,
    version INTEGER NOT NULL,
    subject_template TEXT NOT NULL,
    preview_template TEXT NOT NULL DEFAULT '',
    title_template TEXT NOT NULL DEFAULT '',
    body_template TEXT NOT NULL DEFAULT '',
    button_label_template TEXT NOT NULL DEFAULT '',
    button_url_template TEXT NOT NULL DEFAULT '',
    note_template TEXT NOT NULL DEFAULT '',
    footer_template TEXT NOT NULL DEFAULT '',
    text_template TEXT NOT NULL DEFAULT '',
    required_variables TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    published BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    published_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    published_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_template_versions_lang_check CHECK (lang IN ('id', 'en', 'ar')),
    CONSTRAINT email_template_versions_version_check CHECK (version > 0),
    UNIQUE (template_id, lang, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_template_versions_one_published_lang
    ON email_template_versions(template_id, lang)
    WHERE published = true;

CREATE INDEX IF NOT EXISTS idx_email_template_versions_template_lang
    ON email_template_versions(template_id, lang, version DESC);

CREATE TABLE IF NOT EXISTS email_event_settings (
    key VARCHAR(128) PRIMARY KEY,
    template_id UUID NOT NULL REFERENCES email_templates(id) ON DELETE RESTRICT,
    enabled BOOLEAN NOT NULL DEFAULT true,
    critical BOOLEAN NOT NULL DEFAULT false,
    cooldown_seconds BIGINT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_event_settings_cooldown_check CHECK (cooldown_seconds IS NULL OR cooldown_seconds > 0)
);

CREATE TABLE IF NOT EXISTS email_messages (
    id UUID PRIMARY KEY,
    category VARCHAR(32) NOT NULL,
    template_key VARCHAR(128) NULL,
    template_version_id UUID NULL REFERENCES email_template_versions(id) ON DELETE SET NULL,
    campaign_id UUID NULL,
    campaign_recipient_id UUID NULL,
    user_id UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    recipient_email VARCHAR(255) NOT NULL,
    lang VARCHAR(8) NOT NULL DEFAULT 'id',
    subject TEXT NOT NULL,
    status VARCHAR(32) NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    provider_response TEXT NULL,
    error TEXT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    scheduled_at TIMESTAMP NULL,
    sent_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_messages_category_check CHECK (category IN ('transactional', 'marketing')),
    CONSTRAINT email_messages_status_check CHECK (status IN ('queued', 'sent', 'failed', 'skipped')),
    CONSTRAINT email_messages_lang_check CHECK (lang IN ('id', 'en', 'ar'))
);

CREATE INDEX IF NOT EXISTS idx_email_messages_created
    ON email_messages(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_email_messages_recipient_created
    ON email_messages(lower(recipient_email), created_at DESC);

CREATE INDEX IF NOT EXISTS idx_email_messages_status_created
    ON email_messages(status, created_at DESC);

CREATE TABLE IF NOT EXISTS email_subscriptions (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    marketing_opt_in BOOLEAN NOT NULL DEFAULT false,
    opted_in_at TIMESTAMP NULL,
    opted_out_at TIMESTAMP NULL,
    source VARCHAR(128) NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_email_subscriptions_marketing_opt_in
    ON email_subscriptions(marketing_opt_in)
    WHERE marketing_opt_in = true;

CREATE TABLE IF NOT EXISTS email_suppressions (
    id UUID PRIMARY KEY,
    email VARCHAR(255) NOT NULL,
    email_normalized VARCHAR(255) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    reason VARCHAR(128) NOT NULL,
    created_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_suppressions_scope_check CHECK (scope IN ('marketing', 'all'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_email_suppressions_email_scope
    ON email_suppressions(email_normalized, scope);

CREATE TABLE IF NOT EXISTS email_campaigns (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    template_id UUID NOT NULL REFERENCES email_templates(id) ON DELETE RESTRICT,
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    audience JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    scheduled_at TIMESTAMP NULL,
    sent_at TIMESTAMP NULL,
    cancelled_at TIMESTAMP NULL,
    created_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    updated_by UUID NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_campaigns_status_check CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'cancelled'))
);

CREATE INDEX IF NOT EXISTS idx_email_campaigns_status_schedule
    ON email_campaigns(status, scheduled_at);

CREATE TABLE IF NOT EXISTS email_campaign_recipients (
    id UUID PRIMARY KEY,
    campaign_id UUID NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    lang VARCHAR(8) NOT NULL DEFAULT 'id',
    unsubscribe_url TEXT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    message_id UUID NULL REFERENCES email_messages(id) ON DELETE SET NULL,
    error TEXT NULL,
    sent_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT now(),
    updated_at TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT email_campaign_recipients_status_check CHECK (status IN ('pending', 'sent', 'failed', 'skipped')),
    CONSTRAINT email_campaign_recipients_lang_check CHECK (lang IN ('id', 'en', 'ar')),
    UNIQUE (campaign_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_email_campaign_recipients_campaign_status
    ON email_campaign_recipients(campaign_id, status);

WITH template_seed(id, key, name, category, critical, enabled) AS (
    VALUES
        ('10000000-0000-0000-0000-000000000001'::uuid, 'auth_verification', 'Verify email', 'transactional', true, true),
        ('10000000-0000-0000-0000-000000000002'::uuid, 'auth_password_reset', 'Reset password', 'transactional', true, true),
        ('10000000-0000-0000-0000-000000000003'::uuid, 'auth_email_change_verification', 'Confirm email change', 'transactional', true, true),
        ('10000000-0000-0000-0000-000000000004'::uuid, 'auth_password_changed', 'Password changed', 'transactional', false, true),
        ('10000000-0000-0000-0000-000000000005'::uuid, 'auth_email_verified', 'Email verified', 'transactional', false, true),
        ('10000000-0000-0000-0000-000000000006'::uuid, 'auth_new_login', 'New login alert', 'transactional', false, true),
        ('10000000-0000-0000-0000-000000000007'::uuid, 'auth_failed_login', 'Failed login alert', 'transactional', false, true),
        ('10000000-0000-0000-0000-000000000008'::uuid, 'auth_role_changed', 'Role changed', 'transactional', false, true),
        ('10000000-0000-0000-0000-000000000009'::uuid, 'auth_email_changed', 'Email changed', 'transactional', false, true),
        ('10000000-0000-0000-0000-000000000010'::uuid, 'auth_account_deleted', 'Account deleted', 'transactional', false, true)
)
INSERT INTO email_templates (id, key, name, category, critical, enabled, created_at, updated_at)
SELECT id, key, name, category, critical, enabled, now(), now()
FROM template_seed
ON CONFLICT (key) DO UPDATE SET
    name = EXCLUDED.name,
    critical = EXCLUDED.critical,
    updated_at = now();

INSERT INTO email_event_settings (key, template_id, enabled, critical, created_at, updated_at)
SELECT key, id, enabled, critical, now(), now()
FROM email_templates
WHERE key LIKE 'auth_%'
ON CONFLICT (key) DO UPDATE SET
    template_id = EXCLUDED.template_id,
    critical = EXCLUDED.critical,
    updated_at = now();

WITH version_seed(
    id,
    template_key,
    lang,
    subject_template,
    preview_template,
    title_template,
    body_template,
    button_label_template,
    button_url_template,
    note_template,
    footer_template,
    text_template,
    required_variables
) AS (
    VALUES
        ('20000000-0000-0000-0000-000000000001'::uuid, 'auth_verification', 'id', 'Verifikasi email Surau', 'Selesaikan verifikasi email agar akun Surau Anda siap digunakan.', 'Verifikasi email', 'Assalamu''alaikum, {{.name}}. Konfirmasi alamat email ini agar akun Surau Anda siap digunakan.', 'Verifikasi email', '{{.link}}', 'Link verifikasi ini berlaku selama {{.duration}}.', 'Jika Anda tidak membuat akun Surau, abaikan email ini.', 'Assalamu''alaikum, {{.name}}\n\nKonfirmasi alamat email ini agar akun Surau Anda siap digunakan:\n{{.link}}\n\nLink verifikasi ini berlaku selama {{.duration}}.\n\nJika Anda tidak membuat akun Surau, abaikan email ini.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000002'::uuid, 'auth_verification', 'en', 'Verify your Surau email', 'Verify your Surau email to finish setting up your account.', 'Verify your email', 'Assalamu''alaikum, {{.name}}. Confirm this email address so your Surau account is ready to use.', 'Verify email', '{{.link}}', 'This verification link expires in {{.duration}}.', 'If you did not create a Surau account, you can ignore this email.', 'Assalamu''alaikum, {{.name}}\n\nConfirm this email address so your Surau account is ready to use:\n{{.link}}\n\nThis verification link expires in {{.duration}}.\n\nIf you did not create a Surau account, you can ignore this email.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000003'::uuid, 'auth_verification', 'ar', 'تأكيد بريدك في Surau', 'أكمل تأكيد البريد ليصبح حسابك في Surau جاهزا.', 'تأكيد البريد الإلكتروني', 'السلام عليكم، {{.name}}. أكد هذا البريد الإلكتروني ليصبح حسابك في Surau جاهزا.', 'تأكيد البريد', '{{.link}}', 'تنتهي صلاحية رابط التأكيد خلال {{.duration}}.', 'إذا لم تنشئ حسابا في Surau، يمكنك تجاهل هذه الرسالة.', 'السلام عليكم، {{.name}}\n\nأكد هذا البريد الإلكتروني ليصبح حسابك في Surau جاهزا:\n{{.link}}\n\nتنتهي صلاحية رابط التأكيد خلال {{.duration}}.\n\nإذا لم تنشئ حسابا في Surau، يمكنك تجاهل هذه الرسالة.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000004'::uuid, 'auth_password_reset', 'id', 'Reset password Surau', 'Gunakan link aman ini untuk reset password Surau Anda.', 'Reset password', 'Assalamu''alaikum, {{.name}}. Kami menerima permintaan untuk reset password akun Surau Anda.', 'Reset password', '{{.link}}', 'Link reset password ini berlaku selama {{.duration}}.', 'Jika Anda tidak meminta ini, abaikan email ini.', 'Assalamu''alaikum, {{.name}}\n\nKami menerima permintaan untuk reset password akun Surau Anda:\n{{.link}}\n\nLink reset password ini berlaku selama {{.duration}}.\n\nJika Anda tidak meminta ini, abaikan email ini.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000005'::uuid, 'auth_password_reset', 'en', 'Reset your Surau password', 'Use this secure link to reset your Surau password.', 'Reset your password', 'Assalamu''alaikum, {{.name}}. We received a request to reset the password for your Surau account.', 'Reset password', '{{.link}}', 'This password reset link expires in {{.duration}}.', 'If you did not request this, you can safely ignore this email.', 'Assalamu''alaikum, {{.name}}\n\nWe received a request to reset the password for your Surau account:\n{{.link}}\n\nThis password reset link expires in {{.duration}}.\n\nIf you did not request this, you can safely ignore this email.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000006'::uuid, 'auth_password_reset', 'ar', 'إعادة تعيين كلمة مرور Surau', 'استخدم هذا الرابط الآمن لإعادة تعيين كلمة مرور Surau.', 'إعادة تعيين كلمة المرور', 'السلام عليكم، {{.name}}. وصلنا طلب لإعادة تعيين كلمة مرور حسابك في Surau.', 'إعادة تعيين كلمة المرور', '{{.link}}', 'تنتهي صلاحية رابط إعادة التعيين خلال {{.duration}}.', 'إذا لم تطلب ذلك، يمكنك تجاهل هذه الرسالة بأمان.', 'السلام عليكم، {{.name}}\n\nوصلنا طلب لإعادة تعيين كلمة مرور حسابك في Surau:\n{{.link}}\n\nتنتهي صلاحية رابط إعادة التعيين خلال {{.duration}}.\n\nإذا لم تطلب ذلك، يمكنك تجاهل هذه الرسالة بأمان.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000007'::uuid, 'auth_email_change_verification', 'id', 'Konfirmasi email baru Surau', 'Konfirmasi alamat email ini sebelum menjadi email login Surau Anda.', 'Konfirmasi email baru', 'Assalamu''alaikum, {{.name}}. Konfirmasi email baru untuk akun Surau Anda.', 'Konfirmasi email', '{{.link}}', 'Link ini berlaku selama {{.duration}}.', 'Jika Anda tidak meminta ini, abaikan email ini dan email saat ini tetap digunakan.', 'Assalamu''alaikum, {{.name}}\n\nKonfirmasi email baru untuk akun Surau Anda:\n{{.link}}\n\nLink ini berlaku selama {{.duration}}.\n\nJika Anda tidak meminta ini, abaikan email ini dan email saat ini tetap digunakan.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000008'::uuid, 'auth_email_change_verification', 'en', 'Confirm your new Surau email', 'Confirm this email address before it becomes your Surau login email.', 'Confirm new email', 'Assalamu''alaikum, {{.name}}. Confirm this new email address for your Surau account.', 'Confirm email', '{{.link}}', 'This link expires in {{.duration}}.', 'If you did not request this, ignore this email and keep your current email.', 'Assalamu''alaikum, {{.name}}\n\nConfirm this new email address for your Surau account:\n{{.link}}\n\nThis link expires in {{.duration}}.\n\nIf you did not request this, ignore this email and keep your current email.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000009'::uuid, 'auth_email_change_verification', 'ar', 'تأكيد بريد Surau الجديد', 'أكد هذا البريد قبل أن يصبح بريد الدخول إلى Surau.', 'تأكيد البريد الجديد', 'السلام عليكم، {{.name}}. أكد هذا البريد الإلكتروني الجديد لحسابك في Surau.', 'تأكيد البريد', '{{.link}}', 'تنتهي صلاحية الرابط خلال {{.duration}}.', 'إذا لم تطلب ذلك، فتجاهل هذه الرسالة وسيبقى بريدك الحالي كما هو.', 'السلام عليكم، {{.name}}\n\nأكد هذا البريد الإلكتروني الجديد لحسابك في Surau:\n{{.link}}\n\nتنتهي صلاحية الرابط خلال {{.duration}}.\n\nإذا لم تطلب ذلك، فتجاهل هذه الرسالة وسيبقى بريدك الحالي كما هو.', ARRAY['name','link','duration']),
        ('20000000-0000-0000-0000-000000000010'::uuid, 'auth_password_changed', 'id', 'Password Surau berhasil diubah', 'Password akun Surau Anda baru saja diubah.', 'Password berhasil diubah', 'Assalamu''alaikum, {{.name}}. Password akun Surau Anda baru saja diubah.', '', '', 'Jika ini bukan Anda, segera reset password dari halaman login dan hubungi support.', 'Email keamanan ini dikirim untuk membantu melindungi akun Anda.', 'Assalamu''alaikum, {{.name}}\n\nPassword akun Surau Anda baru saja diubah.\n\nJika ini bukan Anda, segera reset password dari halaman login dan hubungi support.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000011'::uuid, 'auth_password_changed', 'en', 'Your Surau password was changed', 'Your Surau account password was just changed.', 'Password changed', 'Assalamu''alaikum, {{.name}}. Your Surau account password was just changed.', '', '', 'If this was not you, reset your password from the login page and contact support.', 'This security email was sent to help protect your account.', 'Assalamu''alaikum, {{.name}}\n\nYour Surau account password was just changed.\n\nIf this was not you, reset your password from the login page and contact support.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000012'::uuid, 'auth_password_changed', 'ar', 'تم تغيير كلمة مرور Surau', 'تم تغيير كلمة مرور حسابك في Surau للتو.', 'تم تغيير كلمة المرور', 'السلام عليكم، {{.name}}. تم تغيير كلمة مرور حسابك في Surau للتو.', '', '', 'إذا لم تكن أنت من قام بذلك، فأعد تعيين كلمة المرور من صفحة تسجيل الدخول وتواصل مع الدعم.', 'أرسلنا هذه الرسالة الأمنية للمساعدة في حماية حسابك.', 'السلام عليكم، {{.name}}\n\nتم تغيير كلمة مرور حسابك في Surau للتو.\n\nإذا لم تكن أنت من قام بذلك، فأعد تعيين كلمة المرور من صفحة تسجيل الدخول وتواصل مع الدعم.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000013'::uuid, 'auth_email_verified', 'id', 'Email Surau berhasil diverifikasi', 'Email akun Surau Anda sudah diverifikasi dan akun siap digunakan.', 'Email berhasil diverifikasi', 'Assalamu''alaikum, {{.name}}. Email akun Surau Anda sudah diverifikasi dan akun siap digunakan.', '', '', 'Terima kasih sudah menjaga keamanan akun.', 'Jika Anda tidak melakukan verifikasi ini, segera hubungi support.', 'Assalamu''alaikum, {{.name}}\n\nEmail akun Surau Anda sudah diverifikasi dan akun siap digunakan.\n\nTerima kasih sudah menjaga keamanan akun.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000014'::uuid, 'auth_email_verified', 'en', 'Your Surau email was verified', 'Your Surau email is verified and your account is ready to use.', 'Email verified', 'Assalamu''alaikum, {{.name}}. Your Surau email is verified and your account is ready to use.', '', '', 'Thank you for keeping your account secure.', 'If you did not verify this email, contact support.', 'Assalamu''alaikum, {{.name}}\n\nYour Surau email is verified and your account is ready to use.\n\nThank you for keeping your account secure.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000015'::uuid, 'auth_email_verified', 'ar', 'تم تأكيد بريد Surau', 'تم تأكيد بريد حسابك في Surau وأصبح الحساب جاهزا للاستخدام.', 'تم تأكيد البريد', 'السلام عليكم، {{.name}}. تم تأكيد بريد حسابك في Surau وأصبح الحساب جاهزا للاستخدام.', '', '', 'شكرا لك على الحفاظ على أمان حسابك.', 'إذا لم تقم بتأكيد هذا البريد، فتواصل مع الدعم.', 'السلام عليكم، {{.name}}\n\nتم تأكيد بريد حسابك في Surau وأصبح الحساب جاهزا للاستخدام.\n\nشكرا لك على الحفاظ على أمان حسابك.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000016'::uuid, 'auth_new_login', 'id', 'Login baru ke akun Surau', 'Ada login baru ke akun Surau Anda.', 'Login baru terdeteksi', 'Assalamu''alaikum, {{.name}}. Ada login baru ke akun Surau Anda.', '', '', '{{.details}} Jika ini bukan Anda, segera ubah password.', 'Email ini hanya dikirim untuk kombinasi IP/perangkat yang belum pernah terlihat sebelumnya.', 'Assalamu''alaikum, {{.name}}\n\nAda login baru ke akun Surau Anda.\n\n{{.details}} Jika ini bukan Anda, segera ubah password.', ARRAY['name','details']),
        ('20000000-0000-0000-0000-000000000017'::uuid, 'auth_new_login', 'en', 'New login to your Surau account', 'A new login to your Surau account was detected.', 'New login detected', 'Assalamu''alaikum, {{.name}}. A new login to your Surau account was detected.', '', '', '{{.details}} If this was not you, change your password immediately.', 'This email is only sent for an IP/device combination we have not seen before.', 'Assalamu''alaikum, {{.name}}\n\nA new login to your Surau account was detected.\n\n{{.details}} If this was not you, change your password immediately.', ARRAY['name','details']),
        ('20000000-0000-0000-0000-000000000018'::uuid, 'auth_new_login', 'ar', 'تسجيل دخول جديد إلى Surau', 'رصدنا تسجيل دخول جديدا إلى حسابك في Surau.', 'تم رصد تسجيل دخول جديد', 'السلام عليكم، {{.name}}. رصدنا تسجيل دخول جديدا إلى حسابك في Surau.', '', '', '{{.details}} إذا لم تكن أنت من قام بذلك، فغير كلمة المرور فورا.', 'نرسل هذه الرسالة فقط عند ظهور تركيبة IP/جهاز لم نرها من قبل.', 'السلام عليكم، {{.name}}\n\nرصدنا تسجيل دخول جديدا إلى حسابك في Surau.\n\n{{.details}} إذا لم تكن أنت من قام بذلك، فغير كلمة المرور فورا.', ARRAY['name','details']),
        ('20000000-0000-0000-0000-000000000019'::uuid, 'auth_failed_login', 'id', 'Percobaan login Surau dibatasi', 'Percobaan login ke akun Surau Anda sedang dibatasi.', 'Percobaan login dibatasi', 'Assalamu''alaikum, {{.name}}. Kami membatasi percobaan login ke akun Surau Anda karena terlalu banyak percobaan.', '', '', 'Jika ini bukan Anda, reset password dari halaman login.', 'Notifikasi ini dibatasi frekuensinya agar tidak mengganggu.', 'Assalamu''alaikum, {{.name}}\n\nKami membatasi percobaan login ke akun Surau Anda karena terlalu banyak percobaan.\n\nJika ini bukan Anda, reset password dari halaman login.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000020'::uuid, 'auth_failed_login', 'en', 'Surau login attempts were limited', 'Login attempts to your Surau account are currently limited.', 'Login attempts limited', 'Assalamu''alaikum, {{.name}}. We limited login attempts to your Surau account because there were too many tries.', '', '', 'If this was not you, reset your password from the login page.', 'This notification is rate-limited so it does not interrupt you too often.', 'Assalamu''alaikum, {{.name}}\n\nWe limited login attempts to your Surau account because there were too many tries.\n\nIf this was not you, reset your password from the login page.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000021'::uuid, 'auth_failed_login', 'ar', 'تم تقييد محاولات دخول Surau', 'محاولات تسجيل الدخول إلى حسابك في Surau مقيدة حاليا.', 'تم تقييد محاولات الدخول', 'السلام عليكم، {{.name}}. قمنا بتقييد محاولات تسجيل الدخول إلى حسابك في Surau بسبب كثرة المحاولات.', '', '', 'إذا لم تكن أنت من قام بذلك، فأعد تعيين كلمة المرور من صفحة تسجيل الدخول.', 'يتم تحديد تكرار هذا التنبيه حتى لا يزعجك كثيرا.', 'السلام عليكم، {{.name}}\n\nقمنا بتقييد محاولات تسجيل الدخول إلى حسابك في Surau بسبب كثرة المحاولات.\n\nإذا لم تكن أنت من قام بذلك، فأعد تعيين كلمة المرور من صفحة تسجيل الدخول.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000022'::uuid, 'auth_role_changed', 'id', 'Role akun Surau berubah', 'Role akun Surau Anda baru saja berubah.', 'Role akun berubah', 'Assalamu''alaikum, {{.name}}. Role akun Surau Anda berubah menjadi {{.role}}.', '', '', 'Jika perubahan ini tidak Anda kenali, segera hubungi support.', 'Email keamanan ini dikirim karena izin akun berubah.', 'Assalamu''alaikum, {{.name}}\n\nRole akun Surau Anda berubah menjadi {{.role}}.\n\nJika perubahan ini tidak Anda kenali, segera hubungi support.', ARRAY['name','role']),
        ('20000000-0000-0000-0000-000000000023'::uuid, 'auth_role_changed', 'en', 'Your Surau account role changed', 'Your Surau account role was just changed.', 'Account role changed', 'Assalamu''alaikum, {{.name}}. Your Surau account role was changed to {{.role}}.', '', '', 'If you do not recognize this change, contact support.', 'This security email was sent because account permissions changed.', 'Assalamu''alaikum, {{.name}}\n\nYour Surau account role was changed to {{.role}}.\n\nIf you do not recognize this change, contact support.', ARRAY['name','role']),
        ('20000000-0000-0000-0000-000000000024'::uuid, 'auth_role_changed', 'ar', 'تغير دور حساب Surau', 'تم تغيير دور حسابك في Surau للتو.', 'تغير دور الحساب', 'السلام عليكم، {{.name}}. تم تغيير دور حسابك في Surau إلى {{.role}}.', '', '', 'إذا لم تتعرف على هذا التغيير، فتواصل مع الدعم.', 'أرسلنا هذه الرسالة الأمنية لأن صلاحيات الحساب تغيرت.', 'السلام عليكم، {{.name}}\n\nتم تغيير دور حسابك في Surau إلى {{.role}}.\n\nإذا لم تتعرف على هذا التغيير، فتواصل مع الدعم.', ARRAY['name','role']),
        ('20000000-0000-0000-0000-000000000025'::uuid, 'auth_email_changed', 'id', 'Email Surau berhasil diubah', 'Email akun Surau Anda sudah berubah.', 'Email berhasil diubah', 'Assalamu''alaikum, {{.name}}. Email akun Surau Anda sudah berubah.', '', '', 'Email lama: {{.old_email}}. Email baru: {{.new_email}}. Jika ini bukan Anda, segera hubungi support.', 'Email keamanan ini dikirim karena email login akun berubah.', 'Assalamu''alaikum, {{.name}}\n\nEmail akun Surau Anda sudah berubah.\n\nEmail lama: {{.old_email}}. Email baru: {{.new_email}}. Jika ini bukan Anda, segera hubungi support.', ARRAY['name','old_email','new_email']),
        ('20000000-0000-0000-0000-000000000026'::uuid, 'auth_email_changed', 'en', 'Your Surau email was changed', 'The email address for your Surau account was changed.', 'Email changed', 'Assalamu''alaikum, {{.name}}. The email address for your Surau account was changed.', '', '', 'Old email: {{.old_email}}. New email: {{.new_email}}. If this was not you, contact support immediately.', 'This security email was sent because your login email changed.', 'Assalamu''alaikum, {{.name}}\n\nThe email address for your Surau account was changed.\n\nOld email: {{.old_email}}. New email: {{.new_email}}. If this was not you, contact support immediately.', ARRAY['name','old_email','new_email']),
        ('20000000-0000-0000-0000-000000000027'::uuid, 'auth_email_changed', 'ar', 'تم تغيير بريد Surau', 'تم تغيير البريد الإلكتروني لحسابك في Surau.', 'تم تغيير البريد', 'السلام عليكم، {{.name}}. تم تغيير البريد الإلكتروني لحسابك في Surau.', '', '', 'البريد القديم: {{.old_email}}. البريد الجديد: {{.new_email}}. إذا لم تكن أنت من قام بذلك، فتواصل مع الدعم فورا.', 'أرسلنا هذه الرسالة الأمنية لأن بريد تسجيل الدخول تغير.', 'السلام عليكم، {{.name}}\n\nتم تغيير البريد الإلكتروني لحسابك في Surau.\n\nالبريد القديم: {{.old_email}}. البريد الجديد: {{.new_email}}. إذا لم تكن أنت من قام بذلك، فتواصل مع الدعم فورا.', ARRAY['name','old_email','new_email']),
        ('20000000-0000-0000-0000-000000000028'::uuid, 'auth_account_deleted', 'id', 'Akun Surau berhasil dihapus', 'Akun Surau Anda sudah dihapus.', 'Akun berhasil dihapus', 'Assalamu''alaikum, {{.name}}. Akun Surau Anda sudah dihapus.', '', '', 'Jika ini bukan Anda, segera hubungi support.', 'Ini adalah notifikasi keamanan terakhir untuk akun yang dihapus.', 'Assalamu''alaikum, {{.name}}\n\nAkun Surau Anda sudah dihapus.\n\nJika ini bukan Anda, segera hubungi support.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000029'::uuid, 'auth_account_deleted', 'en', 'Your Surau account was deleted', 'Your Surau account was deleted.', 'Account deleted', 'Assalamu''alaikum, {{.name}}. Your Surau account was deleted.', '', '', 'If this was not you, contact support immediately.', 'This is a final security notification for the deleted account.', 'Assalamu''alaikum, {{.name}}\n\nYour Surau account was deleted.\n\nIf this was not you, contact support immediately.', ARRAY['name']),
        ('20000000-0000-0000-0000-000000000030'::uuid, 'auth_account_deleted', 'ar', 'تم حذف حساب Surau', 'تم حذف حسابك في Surau.', 'تم حذف الحساب', 'السلام عليكم، {{.name}}. تم حذف حسابك في Surau.', '', '', 'إذا لم تكن أنت من قام بذلك، فتواصل مع الدعم فورا.', 'هذه رسالة أمان أخيرة للحساب المحذوف.', 'السلام عليكم، {{.name}}\n\nتم حذف حسابك في Surau.\n\nإذا لم تكن أنت من قام بذلك، فتواصل مع الدعم فورا.', ARRAY['name'])
)
INSERT INTO email_template_versions (
    id,
    template_id,
    lang,
    version,
    subject_template,
    preview_template,
    title_template,
    body_template,
    button_label_template,
    button_url_template,
    note_template,
    footer_template,
    text_template,
    required_variables,
    published,
    published_at,
    created_at,
    updated_at
)
SELECT
    version_seed.id,
    email_templates.id,
    version_seed.lang,
    1,
    version_seed.subject_template,
    version_seed.preview_template,
    version_seed.title_template,
    version_seed.body_template,
    version_seed.button_label_template,
    version_seed.button_url_template,
    version_seed.note_template,
    version_seed.footer_template,
    version_seed.text_template,
    version_seed.required_variables,
    true,
    now(),
    now(),
    now()
FROM version_seed
JOIN email_templates ON email_templates.key = version_seed.template_key
ON CONFLICT (template_id, lang, version) DO UPDATE SET
    subject_template = EXCLUDED.subject_template,
    preview_template = EXCLUDED.preview_template,
    title_template = EXCLUDED.title_template,
    body_template = EXCLUDED.body_template,
    button_label_template = EXCLUDED.button_label_template,
    button_url_template = EXCLUDED.button_url_template,
    note_template = EXCLUDED.note_template,
    footer_template = EXCLUDED.footer_template,
    text_template = EXCLUDED.text_template,
    required_variables = EXCLUDED.required_variables,
    published = true,
    published_at = COALESCE(email_template_versions.published_at, now()),
    updated_at = now();
