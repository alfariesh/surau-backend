UPDATE email_template_versions v
SET body_template = replace(
        replace(
            replace(
                v.body_template,
                ' Or enter this 6-digit code: {{.otp}}. The code expires in {{.otp_duration}}.',
                ''
            ),
            ' أو أدخل هذا الرمز المكون من 6 أرقام: {{.otp}}. تنتهي صلاحية الرمز خلال {{.otp_duration}}.',
            ''
        ),
        ' Atau masukkan kode 6 digit ini: {{.otp}}. Kode berlaku selama {{.otp_duration}}.',
        ''
    ),
    text_template = replace(
        replace(
            replace(
                v.text_template,
                '\n\nOr enter this 6-digit code:\n{{.otp}}\n\nThe code expires in {{.otp_duration}}.',
                ''
            ),
            '\n\nأو أدخل هذا الرمز المكون من 6 أرقام:\n{{.otp}}\n\nتنتهي صلاحية الرمز خلال {{.otp_duration}}.',
            ''
        ),
        '\n\nAtau masukkan kode 6 digit ini:\n{{.otp}}\n\nKode berlaku selama {{.otp_duration}}.',
        ''
    ),
    required_variables = array_remove(array_remove(v.required_variables, 'otp'), 'otp_duration'),
    updated_at = now()
FROM email_templates t
WHERE v.template_id = t.id
    AND t.key IN ('auth_verification', 'auth_email_change_verification');

ALTER TABLE email_change_tokens
    DROP COLUMN IF EXISTS otp_expires_at,
    DROP COLUMN IF EXISTS otp_hash;

ALTER TABLE email_verification_tokens
    DROP COLUMN IF EXISTS otp_expires_at,
    DROP COLUMN IF EXISTS otp_hash;
