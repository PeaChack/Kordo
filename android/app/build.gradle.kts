import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("io.github.takahirom.roborazzi") version "1.43.0"
}

// Version from build/version.txt or -PkordoVersion=0.3.0 (CI passes the tag).
val kordoVersion: String = (findProperty("kordoVersion") as String?)
    ?: rootProject.file("../build/version.txt").readText().trim()

// versionCode from a.b.c: a*1_000_000 + b*1_000 + c (prerelease suffix ignored)
val kordoVersionCode: Int = kordoVersion.substringBefore('-').substringBefore('+')
    .split('.').map { it.toIntOrNull() ?: 0 }.let { p ->
        (p.getOrElse(0) { 0 } * 1_000_000) + (p.getOrElse(1) { 0 } * 1_000) + p.getOrElse(2) { 0 }
    }.coerceAtLeast(1)

// Release signing: keystore from environment (CI secrets) or keystore.properties.
val signingProps = Properties().apply {
    val f = rootProject.file("keystore.properties")
    if (f.exists()) f.inputStream().use { load(it) }
}
fun signingValue(env: String, prop: String): String? = System.getenv(env) ?: signingProps.getProperty(prop)

android {
    namespace = "app.kordo.android"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.kordo.android"
        minSdk = 26
        targetSdk = 35
        versionCode = kordoVersionCode
        versionName = kordoVersion
    }

    signingConfigs {
        val store = signingValue("KORDO_KEYSTORE", "storeFile")
        if (store != null) {
            create("release") {
                storeFile = file(store)
                storePassword = signingValue("KORDO_KEYSTORE_PASSWORD", "storePassword")
                keyAlias = signingValue("KORDO_KEY_ALIAS", "keyAlias")
                keyPassword = signingValue("KORDO_KEY_PASSWORD", "keyPassword")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    testOptions {
        unitTests {
            isIncludeAndroidResources = true
            all { test ->
                // -ProbolectricRepo=<maven mirror> where Maven Central is rate-limited
                (findProperty("robolectricRepo") as String?)?.let { test.systemProperty("robolectric.dependency.repo.url", it) }
            }
        }
    }

    packaging {
        jniLibs { useLegacyPackaging = true }
    }
}

dependencies {
    // Go core (mobile/) + sing-box libbox, built by `gomobile bind` (see docs/DEVELOPMENT.md)
    implementation(files("libs/kordo.aar"))

    val composeBom = platform("androidx.compose:compose-bom:2025.05.01")
    implementation(composeBom)
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material:material-icons-core")
    implementation("androidx.activity:activity-compose:1.10.1")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.9.0")
    implementation("androidx.core:core-ktx:1.16.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.10.2")

    // Screenshot tests on the JVM (no device): ./gradlew recordRoborazziDebug
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.robolectric:robolectric:4.15.1")
    testImplementation("io.github.takahirom.roborazzi:roborazzi:1.43.0")
    testImplementation("io.github.takahirom.roborazzi:roborazzi-compose:1.43.0")
    testImplementation(composeBom)
    testImplementation("androidx.compose.ui:ui-test-junit4")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
