// Adapted from Svix webhook_http_client.rs; upstream revision 343b15d3658c1d9bffe7b4a1b69465d5a25cc893
// https://github.com/svix/svix-webhooks/blob/343b15d3658c1d9bffe7b4a1b69465d5a25cc893/server/svix-server/src/core/webhook_http_client.rs
// Only visibility changed; original classifier and helpers retained.
// The MIT License
//
// Copyright (c) 2021 Svix (https://www.svix.com)
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

use ipnet::{Ipv4Net, Ipv6Net};
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};

pub(super) fn is_allowed(addr: IpAddr) -> bool {
    match addr.to_canonical() {
        IpAddr::V4(addr) => {
            !(addr.is_private()
                || addr.is_loopback()
                || addr.is_link_local()
                || addr.is_broadcast()
                || addr.is_multicast()
                || addr.is_documentation()
                || is_shared(addr)
                || is_reserved(addr)
                || is_benchmarking(addr)
                || starts_with_zero(addr))
        }
        IpAddr::V6(addr) => {
            !(addr.is_multicast()
                || addr.is_loopback()
                || addr.is_unspecified()
                || is_unicast_link_local(addr)
                || is_unique_local(addr)
                || is_documentation_v6(addr)
                || is_discard_v6(addr)
                || is_6to4_or_nat64_v6(addr)
                || is_srv6(addr))
        }
    }
}

#[inline(always)]
fn is_shared(addr: Ipv4Addr) -> bool {
    static CGNAT: Ipv4Net = Ipv4Net::new_assert(Ipv4Addr::new(100, 64, 0, 0), 10);

    CGNAT.contains(&addr)
}

#[inline(always)]
fn is_reserved(addr: Ipv4Addr) -> bool {
    static IETF_RESERVED_1: Ipv4Net = Ipv4Net::new_assert(Ipv4Addr::new(192, 0, 0, 0), 24);
    static IETF_RESERVED_2: Ipv4Net = Ipv4Net::new_assert(Ipv4Addr::new(192, 88, 99, 0), 24);
    static CLASS_E: Ipv4Net = Ipv4Net::new_assert(Ipv4Addr::new(240, 0, 0, 0), 4);

    // note: revisit allowing class E addresses in the future when ipocalypse gets more dire

    IETF_RESERVED_1.contains(&addr) || IETF_RESERVED_2.contains(&addr) || CLASS_E.contains(&addr)
}

#[inline(always)]
fn is_benchmarking(addr: Ipv4Addr) -> bool {
    static BENCHMARKING: Ipv4Net = Ipv4Net::new_assert(Ipv4Addr::new(198, 18, 0, 0), 15);

    BENCHMARKING.contains(&addr)
}

#[inline(always)]
fn starts_with_zero(addr: Ipv4Addr) -> bool {
    addr.octets()[0] == 0
}

#[inline(always)]
fn is_unicast_link_local(addr: Ipv6Addr) -> bool {
    // technically only fe80::/64 are link-local, but fe80::/10 are all reserved for it
    static V6_LL: Ipv6Net = Ipv6Net::new_assert(Ipv6Addr::new(0xfe80, 0, 0, 0, 0, 0, 0, 0), 10);

    V6_LL.contains(&addr)
}

#[inline(always)]
fn is_unique_local(addr: Ipv6Addr) -> bool {
    static ULA: Ipv6Net = Ipv6Net::new_assert(Ipv6Addr::new(0xfc00, 0, 0, 0, 0, 0, 0, 0), 7);

    ULA.contains(&addr)
}

#[inline(always)]
fn is_documentation_v6(addr: Ipv6Addr) -> bool {
    static V6_DOCUMENTATION_1: Ipv6Net =
        Ipv6Net::new_assert(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 0), 32);
    static V6_DOCUMENTATION_2: Ipv6Net =
        Ipv6Net::new_assert(Ipv6Addr::new(0x3fff, 0, 0, 0, 0, 0, 0, 0), 20);

    V6_DOCUMENTATION_1.contains(&addr) || V6_DOCUMENTATION_2.contains(&addr)
}

#[inline(always)]
fn is_discard_v6(addr: Ipv6Addr) -> bool {
    static V6_DISCARD: Ipv6Net = Ipv6Net::new_assert(Ipv6Addr::new(0x100, 0, 0, 0, 0, 0, 0, 0), 64);

    V6_DISCARD.contains(&addr)
}

#[inline(always)]
fn is_6to4_or_nat64_v6(addr: Ipv6Addr) -> bool {
    static IP6TO4: Ipv6Net = Ipv6Net::new_assert(Ipv6Addr::new(0x2002, 0, 0, 0, 0, 0, 0, 0), 16);
    static NAT64: Ipv6Net = Ipv6Net::new_assert(Ipv6Addr::new(0x64, 0xff9b, 0, 0, 0, 0, 0, 0), 96);
    static LOCAL_NAT64: Ipv6Net =
        Ipv6Net::new_assert(Ipv6Addr::new(0x64, 0xff9b, 0x1, 0, 0, 0, 0, 0), 48);

    IP6TO4.contains(&addr) || NAT64.contains(&addr) || LOCAL_NAT64.contains(&addr)
}

#[inline(always)]
fn is_srv6(addr: Ipv6Addr) -> bool {
    static SRV6: Ipv6Net = Ipv6Net::new_assert(Ipv6Addr::new(0x5f00, 0, 0, 0, 0, 0, 0, 0), 16);

    SRV6.contains(&addr)
}
