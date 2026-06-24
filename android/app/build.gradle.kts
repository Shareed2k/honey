plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.hilt)
    kotlin("kapt")
}

android {
    namespace = "com.honey.mobile"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.honey.mobile"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }

    buildFeatures {
        compose = true
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}


dependencies {
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.material3)
    implementation(libs.compose.activity)
    implementation(libs.hilt.android)
    kapt(libs.hilt.compiler)
    implementation(files("libs/honey.aar"))
    implementation(libs.coroutines.android)
    implementation(libs.core.ktx)
    implementation(libs.nav.compose)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.compose.material.icons)
    implementation(libs.biometric)
    implementation(libs.appcompat)
    implementation(libs.room.runtime)
    implementation(libs.room.ktx)
    kapt(libs.room.compiler)
    implementation(libs.security.crypto)
    implementation(libs.gson)
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.8.1")
}

tasks.register<Exec>("buildHoneyAar") {
    group = "build"
    description = "Builds the honey.aar library using gomobile"
    
    val repoRoot = rootProject.projectDir.parentFile
    
    inputs.dir(File(repoRoot, "pkg/mobile"))
    inputs.file(File(repoRoot, "scripts/build-android.sh"))
    outputs.file(File(projectDir, "libs/honey.aar"))
    
    workingDir = repoRoot
    commandLine("bash", "scripts/build-android.sh")
}

afterEvaluate {
    tasks.named("preBuild") {
        dependsOn("buildHoneyAar")
    }
}
