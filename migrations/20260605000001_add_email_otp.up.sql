ALTER TABLE email_verification_tokens
    ADD COLUMN IF NOT EXISTS otp_hash TEXT NULL,
    ADD COLUMN IF NOT EXISTS otp_expires_at TIMESTAMP NULL;

ALTER TABLE email_change_tokens
    ADD COLUMN IF NOT EXISTS otp_hash TEXT NULL,
    ADD COLUMN IF NOT EXISTS otp_expires_at TIMESTAMP NULL;

UPDATE email_template_versions v
SET body_template = CASE v.lang
        WHEN 'en' THEN v.body_template || ' Or enter this 6-digit code: {{.otp}}. The code expires in {{.otp_duration}}.'
        WHEN 'ar' THEN v.body_template || ' أو أدخل هذا الرمز المكون من 6 أرقام: {{.otp}}. تنتهي صلاحية الرمز خلال {{.otp_duration}}.'
        ELSE v.body_template || ' Atau masukkan kode 6 digit ini: {{.otp}}. Kode berlaku selama {{.otp_duration}}.'
    END,
    text_template = CASE v.lang
        WHEN 'en' THEN v.text_template || '\n\nOr enter this 6-digit code:\n{{.otp}}\n\nThe code expires in {{.otp_duration}}.'
        WHEN 'ar' THEN v.text_template || '\n\nأو أدخل هذا الرمز المكون من 6 أرقام:\n{{.otp}}\n\nتنتهي صلاحية الرمز خلال {{.otp_duration}}.'
        ELSE v.text_template || '\n\nAtau masukkan kode 6 digit ini:\n{{.otp}}\n\nKode berlaku selama {{.otp_duration}}.'
    END,
    required_variables = CASE
        WHEN NOT ('otp' = ANY(v.required_variables)) AND NOT ('otp_duration' = ANY(v.required_variables))
            THEN v.required_variables || ARRAY['otp', 'otp_duration']
        WHEN NOT ('otp' = ANY(v.required_variables))
            THEN v.required_variables || ARRAY['otp']
        WHEN NOT ('otp_duration' = ANY(v.required_variables))
            THEN v.required_variables || ARRAY['otp_duration']
        ELSE v.required_variables
    END,
    updated_at = now()
FROM email_templates t
WHERE v.template_id = t.id
    AND t.key IN ('auth_verification', 'auth_email_change_verification')
    AND v.body_template NOT LIKE '%{{.otp}}%'
    AND v.text_template NOT LIKE '%{{.otp}}%';
