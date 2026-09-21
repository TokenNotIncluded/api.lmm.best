//! Pure, field-level three-way restore. This module performs no file writes.
//!
//! Arrays and secrets require adapter-specific ownership; broad object replacement
//! is deliberately rejected. A restore never renews any authorization.

use serde_json::Value;
use thiserror::Error;

/// One scalar field owned by a particular LMM operation.
/// This may contain secrets and therefore intentionally does not implement Debug or Serialize.
pub struct FieldChange {
    /// JSON pointer identifying an existing parent object and scalar leaf.
    pub pointer: String,
    /// Value before the operation; None means an absent property.
    pub before: Option<Value>,
    /// Value written by the operation; None means deletion.
    pub after: Option<Value>,
}

/// Restore errors disclose only the category, never field contents.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum RestoreError {
    /// A newer user or manager edit takes precedence over the backup.
    #[error("字段在 LMM 修改后再次变化，恢复已暂停")]
    Conflict,
    /// Object/array replacement would have an overly broad scope.
    #[error("仅支持对象中的单个标量字段；集合需要专用适配器")]
    UnsupportedShape,
    /// Invalid JSON pointer or missing parent.
    #[error("无法定位恢复字段的父对象")]
    InvalidPointer,
    /// A malformed or overlapping journal must not reorder writes.
    #[error("变更记录包含重复字段")]
    DuplicateField,
}

fn scalar(value: &Option<Value>) -> bool {
    value
        .as_ref()
        .is_none_or(|value| !value.is_object() && !value.is_array())
}

fn decode_key(value: &str) -> Result<String, RestoreError> {
    let mut result = String::new();
    let mut chars = value.chars();
    while let Some(c) = chars.next() {
        if c != '~' {
            result.push(c);
            continue;
        }
        match chars.next() {
            Some('0') => result.push('~'),
            Some('1') => result.push('/'),
            _ => return Err(RestoreError::InvalidPointer),
        }
    }
    Ok(result)
}

/// Revert only unchanged LMM-written scalar fields, preserving later user edits.
///
/// All changes are validated on a clone. Any conflict leaves the input untouched.
/// The caller must separately establish journal ownership and a native transaction
/// boundary before persisting this result. No token or grant is ever reactivated.
pub fn restore_fields(current: &Value, changes: &[FieldChange]) -> Result<Value, RestoreError> {
    let mut output = current.clone();
    let mut seen = std::collections::BTreeSet::new();
    for change in changes {
        if !seen.insert(&change.pointer) {
            return Err(RestoreError::DuplicateField);
        }
        if !scalar(&change.before) || !scalar(&change.after) {
            return Err(RestoreError::UnsupportedShape);
        }
        let (parent, raw_key) = change
            .pointer
            .rsplit_once('/')
            .ok_or(RestoreError::InvalidPointer)?;
        if !change.pointer.starts_with('/') {
            return Err(RestoreError::InvalidPointer);
        }
        let key = decode_key(raw_key)?;
        // Array indices are not stable identities across later user edits.
        let mut node = &mut output;
        for component in parent.split('/').skip(1) {
            let component = decode_key(component)?;
            node = node
                .as_object_mut()
                .ok_or(RestoreError::UnsupportedShape)?
                .get_mut(&component)
                .ok_or(RestoreError::InvalidPointer)?;
        }
        let object = node.as_object_mut().ok_or(RestoreError::InvalidPointer)?;
        let existing = object.get(&key);
        if existing == change.before.as_ref() {
            continue;
        }
        if existing != change.after.as_ref() {
            return Err(RestoreError::Conflict);
        }
        match &change.before {
            Some(value) => {
                object.insert(key, value.clone());
            }
            None => {
                object.remove(&key);
            }
        }
    }
    Ok(output)
}
