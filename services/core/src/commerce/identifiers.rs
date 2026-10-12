//! Identifiers emitted by the Go store. This does not relax the operation,
//! shop, order, payment hash or delivery-reference checks in commerce.rs.

pub(super) fn variant_id(s: &str) -> bool {
    if super::hash_id(s) {
        return true;
    }
    // legacyRandomID creates lowercase RFC 4122 version-4 UUIDs. Preserve the
    // exact bytes in the command; never normalize identifiers before hashing.
    let b = s.as_bytes();
    b.len() == 36
        && b[14] == b'4'
        && matches!(b[19], b'8' | b'9' | b'a' | b'b')
        && b.iter().enumerate().all(|(i, &c)| match i {
            8 | 13 | 18 | 23 => c == b'-',
            _ => c.is_ascii_digit() || (b'a'..=b'f').contains(&c),
        })
}

#[cfg(test)]
mod tests {
    use super::variant_id;
    use super::super::{Error, MoneyCommand, hex, id};
    use sha2::{Digest, Sha256};

    const GO_COMMAND: &[u8] = include_bytes!("testdata/go_uuid_command.json");

    fn command() -> MoneyCommand {
        let [command]: [MoneyCommand; 1] = serde_json::from_slice(GO_COMMAND).unwrap();
        command
    }

    #[test]
    fn accepts_exact_go_payment_with_uuid_variant() {
        // Exported by the Go ChangeOrder funds-port fixture. These are
        // synthetic credits, not a claim that a ledger journal has been posted.
        assert_eq!(
            hex(&Sha256::digest(GO_COMMAND)),
            "76cf86cce8b0ba3ba7d850c13d7b84f83e21ddfe108328f953db959bbf37f54a"
        );
        assert_eq!(command().validate(), Ok(1_000_000));
    }

    #[test]
    fn preserves_native_hash_variant_ids() {
        let mut c = command();
        c.order_snapshot.variant_id = "a".repeat(64);
        assert_eq!(c.validate(), Ok(1_000_000));
    }

    #[test]
    fn uuid_permission_does_not_extend_to_shop_or_order_ids() {
        for replace_shop in [false, true] {
            let mut c = command();
            if replace_shop {
                c.shop_id = c.order_snapshot.variant_id.clone();
            } else {
                c.order_id = c.order_snapshot.variant_id.clone();
            }
            c.key = id(&serde_json::json!([
                "store-money-v1", c.shop_id, c.order_id, c.kind
            ]));
            assert_eq!(c.validate(), Err(Error::Invalid));
        }
    }

    #[test]
    fn rejects_noncanonical_or_wrong_uuid_versions() {
        for bad in [
            "", "00000000-0000-0000-0000-000000000000",
            "e7317c94-7d0a-1a52-9b4f-0c14f811e8e2",
            "e7317c94-7d0a-4a52-7b4f-0c14f811e8e2",
            "E7317C94-7D0A-4A52-9B4F-0C14F811E8E2",
            "e7317c947d0a4a529b4f0c14f811e8e2",
            "../e7317c94-7d0a-4a52-9b4f-0c14f811e8e2",
            "e7317c94-7d0a-4a52-9b4f-0c14f811e8e2\n",
        ] {
            assert!(!variant_id(bad), "accepted {bad:?}");
        }
        assert!(!variant_id(&"A".repeat(64)));
    }

    #[test]
    fn uuid_variant_does_not_bypass_frozen_quantity_or_price() {
        let mut c = command();
        c.order_snapshot.quantity += 1;
        assert_eq!(c.validate(), Err(Error::Invalid));
        let mut c = command();
        c.order_snapshot.created_by = 0;
        assert_eq!(c.validate(), Err(Error::Invalid));
    }
}
