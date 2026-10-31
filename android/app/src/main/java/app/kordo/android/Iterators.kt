package app.kordo.android

import app.kordo.core.libbox.NetworkInterface
import app.kordo.core.libbox.NetworkInterfaceIterator
import app.kordo.core.libbox.RoutePrefixIterator
import app.kordo.core.libbox.StringIterator

class StringArrayIterator(private val items: List<String>) : StringIterator {
    private var i = 0
    override fun len(): Int = items.size
    override fun hasNext(): Boolean = i < items.size
    override fun next(): String = items[i++]
}

class InterfaceArrayIterator(private val items: List<NetworkInterface>) : NetworkInterfaceIterator {
    private var i = 0
    override fun hasNext(): Boolean = i < items.size
    override fun next(): NetworkInterface = items[i++]
}

fun StringIterator?.toList(): List<String> {
    val out = mutableListOf<String>()
    if (this == null) return out
    while (hasNext()) out.add(next())
    return out
}

fun RoutePrefixIterator?.forEachPrefix(fn: (address: String, prefix: Int) -> Unit) {
    if (this == null) return
    while (hasNext()) {
        val p = next()
        fn(p.address(), p.prefix())
    }
}
